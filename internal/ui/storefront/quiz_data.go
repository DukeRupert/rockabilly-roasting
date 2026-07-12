package storefront

// The coffee personality quiz ("What's Your Roast?") sorts visitors into one
// of four archetypes and recommends a real subscribable coffee for each.
// Scoring happens client-side (Alpine); the archetype copy, questions, and
// product matching live here so the templ stays markup-only.

// quizArchetypeKey values double as Alpine score keys — keep them in sync
// with the pick() calls rendered in quiz.templ.
const (
	quizGreaser     = "greaser"
	quizCruiser     = "cruiser"
	quizFirecracker = "firecracker"
	quizNightOwl    = "nightowl"
)

// Score ties resolve deterministically in this order (first listed wins);
// the order is mirrored in the Alpine finish() function in quiz.templ.
var quizArchetypeOrder = []string{quizGreaser, quizCruiser, quizFirecracker, quizNightOwl}

type quizAnswer struct {
	Label string
	Key   string
}

type quizQuestion struct {
	Prompt  string
	Answers []quizAnswer
}

type quizArchetype struct {
	Key     string
	Name    string // display name, e.g. "The Greaser"
	Accent  string // script garnish word on the result card
	Blurb   string
	Profile []string // short profile chips, e.g. "Dark roast"
}

func quizQuestions() []quizQuestion {
	return []quizQuestion{
		{
			Prompt: "The alarm goes off at 6 A.M. What's the move?",
			Answers: []quizAnswer{
				{Label: "Up before it rings. Boots on, let's ride.", Key: quizGreaser},
				{Label: "One snooze. Two is for amateurs.", Key: quizCruiser},
				{Label: "Straight to the window — what's today look like?", Key: quizFirecracker},
				{Label: "6 A.M.? I saw 6 A.M. on my way to bed.", Key: quizNightOwl},
			},
		},
		{
			Prompt: "Pick your ride.",
			Answers: []quizAnswer{
				{Label: "A '59 pickup — loud, black, runs on spite", Key: quizGreaser},
				{Label: "Cream-and-chrome cruiser, waxed every Sunday", Key: quizCruiser},
				{Label: "Whatever's fastest. Windows down either way.", Key: quizFirecracker},
				{Label: "The late train. I like watching the city roll by.", Key: quizNightOwl},
			},
		},
		{
			Prompt: "The jukebox is yours. What's playing?",
			Answers: []quizAnswer{
				{Label: "Johnny Cash — low, slow, and mean", Key: quizGreaser},
				{Label: "Elvis and doo-wop, the classics never miss", Key: quizCruiser},
				{Label: "Wanda Jackson — rockabilly with teeth", Key: quizFirecracker},
				{Label: "Slow blues, after everyone's gone home", Key: quizNightOwl},
			},
		},
		{
			Prompt: "How do you take your coffee?",
			Answers: []quizAnswer{
				{Label: "Black. Next question.", Key: quizGreaser},
				{Label: "A latte — around here, that's practically the law", Key: quizCruiser},
				{Label: "Pour-over, no rush. Tell me about the farm.", Key: quizFirecracker},
				{Label: "Late. The later the better.", Key: quizNightOwl},
			},
		},
		{
			Prompt: "Saturday night looks like…",
			Answers: []quizAnswer{
				{Label: "Backyard fire and tall tales", Key: quizGreaser},
				{Label: "Diner booth, milkshake, the usual crowd", Key: quizCruiser},
				{Label: "A road trip somewhere we've never been", Key: quizFirecracker},
				{Label: "Records on, world off", Key: quizNightOwl},
			},
		},
		{
			Prompt: "Pick a tattoo.",
			Answers: []quizAnswer{
				{Label: "Dagger and rose — old grudges, good ink", Key: quizGreaser},
				{Label: "A classic anchor. Old school never dies.", Key: quizCruiser},
				{Label: "A swallow mid-dive — built for speed", Key: quizFirecracker},
				{Label: "A moth circling the moon", Key: quizNightOwl},
			},
		},
	}
}

func quizArchetypes() []quizArchetype {
	return []quizArchetype{
		{
			Key:    quizGreaser,
			Name:   "The Greaser",
			Accent: "dark & loud.",
			Blurb:  "Bold, no-nonsense, first one up and last one to quit. You like your coffee like your engine — dark, loud, and running hot. Sweet talk is for other people's cups.",
			Profile: []string{
				"Dark roast", "Full body", "Zero fuss",
			},
		},
		{
			Key:    quizCruiser,
			Name:   "The Cruiser",
			Accent: "smooth & steady.",
			Blurb:  "Steady hands, clean chrome, same booth every Saturday. You don't chase trends — you perfected your order years ago and it hasn't let you down since. Smooth, balanced, dependable as a flathead V8.",
			Profile: []string{
				"Medium roast", "Balanced", "Dependable",
			},
		},
		{
			Key:    quizFirecracker,
			Name:   "The Firecracker",
			Accent: "bright & quick.",
			Blurb:  "You take the back roads on purpose. Bright, lively, always chasing the next flavor down the highway — berries, florals, whatever the roast is packing, you want to taste all of it.",
			Profile: []string{
				"Light roast", "Bright acidity", "Big personality",
			},
		},
		{
			Key:    quizNightOwl,
			Name:   "The Night Owl",
			Accent: "cool & mellow.",
			Blurb:  "The city goes quiet and that's when you get going. You're in it for the ritual — the warm cup, the record spinning at midnight. Smooth, mellow, and in no hurry at all.",
			Profile: []string{
				"Smooth & mellow", "Late-night ritual", "Slow burner",
			},
		},
	}
}

// quizRecommendations picks one subscribable coffee per archetype from the
// live catalog. Preference cascades per archetype; distinct products are
// preferred, but a product may repeat when the catalog is small. Missing
// entries (empty catalog) render a browse-all fallback instead of a card.
func quizRecommendations(cards []SubscriptionProductCard) map[string]*SubscriptionProductCard {
	isDecaf := func(c *SubscriptionProductCard) bool {
		return c.Coffee != nil && c.Coffee.IsDecaf
	}
	roastIn := func(levels ...int) func(*SubscriptionProductCard) bool {
		return func(c *SubscriptionProductCard) bool {
			if isDecaf(c) || c.Coffee == nil {
				return false
			}
			idx := roastLevelIndex(c.Coffee.RoastLevel)
			for _, l := range levels {
				if idx == l {
					return true
				}
			}
			return false
		}
	}
	anyCard := func(*SubscriptionProductCard) bool { return true }

	used := map[string]bool{}
	pick := func(preds ...func(*SubscriptionProductCard) bool) *SubscriptionProductCard {
		// Two passes: first honor the used-set so archetypes get distinct
		// coffees, then allow repeats rather than returning nothing.
		for _, allowRepeat := range []bool{false, true} {
			for _, pred := range preds {
				for i := range cards {
					c := &cards[i]
					if (allowRepeat || !used[c.Product.Slug]) && pred(c) {
						used[c.Product.Slug] = true
						return c
					}
				}
			}
		}
		return nil
	}

	// Night Owl deliberately skips decaf (the shop would rather sell the
	// caffeinated evening cup) — it gets a smooth medium-dark instead.
	nonDecaf := func(c *SubscriptionProductCard) bool { return !isDecaf(c) }
	return map[string]*SubscriptionProductCard{
		quizNightOwl:    pick(roastIn(4), roastIn(3), nonDecaf, anyCard),
		quizGreaser:     pick(roastIn(5), roastIn(4), anyCard),
		quizFirecracker: pick(roastIn(1), roastIn(2), roastIn(3), anyCard),
		quizCruiser:     pick(roastIn(3), roastIn(2, 4), anyCard),
	}
}
