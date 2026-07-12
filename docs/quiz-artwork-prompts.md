# Roast Quiz Result Artwork — Midjourney Prompts

Image-generation prompts for the four archetype "crest" illustrations on the
`/quiz` result cards (see `internal/ui/storefront/quiz.templ`). Think Harry
Potter house crests, rendered in the brand's American-traditional-tattoo
language.

**Framing note:** the design system reserves the shield/badge curve for the
RRC brand mark, so these are **circular tattoo-flash medallions**, not
shields — they read as crests without competing with the logo.

## Shared style suffix

Append to every prompt so the set reads as one artist's flash sheet:

> American traditional tattoo flash style, bold black linework, vintage
> letterpress badge, limited palette of warm bone paper #F6EFE1, tattoo black
> ink #0E0D0C, candle amber #F2A03D, rust red #B4351D, aged paper texture,
> halftone shading, screen print, flat 2D emblem, centered circular medallion
> composition, no text, no gradients --ar 1:1 --v 6 --style raw

## The Greaser — *dark & loud*

> Circular tattoo flash medallion of a flaming coffee mug crossed with a
> dagger and rose, black '59 pickup truck silhouette in the background,
> pompadour skull wearing a grease-slicked ducktail, engine flames curling
> around the border, bold rust red and tattoo black with amber flame
> highlights on bone paper

## The Cruiser — *smooth & steady*

> Circular tattoo flash medallion of a classic anchor wrapped around a
> steaming latte cup, chrome-fender cruiser motorcycle detail, diner
> checkerboard trim ring around the border, laurel of coffee branches with
> cherries, clean balanced symmetrical composition, warm cream and tattoo
> black with amber and rust accents on bone paper

## The Firecracker — *bright & quick*

> Circular tattoo flash medallion of a swallow mid-dive clutching a coffee
> cherry branch, lightning bolts and radiating speed lines, winding back-road
> highway ribbon across the lower border, bursting firework sparks rendered
> as halftone dots, energetic asymmetric motion, hot rust red and candle
> amber on bone paper with tattoo black linework

## The Night Owl — *cool & mellow*

> Circular tattoo flash medallion of a moth with open wings circling a
> crescent moon, steaming coffee cup on a spinning vinyl record below,
> scattered stars and a lit candle, quiet nocturnal mood, heavy tattoo black
> fills with candle amber moon glow and a single rust accent on bone paper

## Generation tips

- Generate all four in one session and reroll toward matching line weights so
  the set feels like one flash sheet.
- Keep "no text" — Midjourney mangles lettering, and the archetype names are
  already set in Alfa Slab One on the result card.
- Warm/sepia cast is on-brand; kill any blue-toned results.
- Placement when finals exist: medallion above the "★ THE VERDICT ★" badge on
  each result card, with the signature -2° tilt and stamp shadow. Ship as
  static assets under `internal/ui/assets/`.
