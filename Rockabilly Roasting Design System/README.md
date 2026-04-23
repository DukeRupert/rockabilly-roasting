# Rockabilly Roasting Co. — Design System

> A great bike, a rebel attitude, and a love of America.
> Coffee beans roasted to perfection empowers.

Rockabilly Roasting Co. is a coffee roaster and cafe in **Richland, WA**. The brand is rooted in classic Americana and the rockabilly spirit — think 1950s diners, motorcycle gang crests, hot rod pinstripes, tattoo flash, route 66 roadside signage, and the kind of coffee mug that's been refilled a thousand times.

This design system exists so any agent or designer can create on-brand interfaces, print pieces, merch, slides, and marketing for the brand without reinventing the visual language each time.

---

## Source materials provided

All originals live in `uploads/` (and copies in `assets/`):

| File | What it is |
|---|---|
| `rockabilly-logo.png` | Primary **shield badge** logo — "ROCKABILLY / ROASTING / RR / CO." in a hand-drawn crest. Black line art on transparent. |
| `2026 Rockabilly Badge Logo - Black - Transparent Background.jpg` | 2026 badge, black on transparent (JPEG — the alpha reads as pure black). |
| `2026 Rockabilly Badge Logo - White - Transparent Background.png` | 2026 badge, white on transparent — for dark backgrounds. |
| `Double Candle Clean for Embroidery-RRC.png` | **Double candle + laurel + banner** illustration. Tattoo-flash style, intended for embroidery. Uses amber/orange, white, black. This is the brand's most expressive visual. |
| `Double Candle Clean for Embroidery_RRC.pdf` | Vector version of the candle illustration. |

No codebase, website URL, Figma file, or product screenshots were provided. **The visual system below is extrapolated from the logos + illustrations + brand description.** Anything marked 🚩 is a substitution or assumption that the user should confirm.

---

## CONTENT FUNDAMENTALS

**Voice.** Confident, a little swaggering, warm. Talks to you like the guy behind the counter who remembers your order. Not precious about coffee. Not corporate. Not ironic. Genuinely loves the craft and the life.

**Person.** Mostly **"we"** for the shop, **"you"** for the customer. Occasionally **"your barista"** or **"the roaster."** First name basis energy.

**Casing.**
- Headlines and signage → **ALL CAPS**, wide tracking. Like enamel signs and gas-station marquees.
- Ribbon/banner phrases → Title Case, tight.
- Body copy → sentence case. Short sentences. Plainspoken.
- Labels (menu prices, nav) → UPPERCASE, condensed.

**Punctuation.** Em-dashes used liberally — like this. Ellipses sparingly. No Oxford debate; use one. Exclamation points allowed but earned (one per page, tops).

**Emoji.** **No.** Never. The brand uses hand-drawn flourishes, stars, and swashes instead — if you need a "decoration," reach for `★`, `✦`, `❖`, or an actual asset from `/assets`.

**Unicode characters.** Dingbats are fair game as typographic flourishes: `★ ✦ ✧ ❖ ◆ ✵ ⚞ ⚟ ⬥`. Great for menu dividers. Not as icons.

**Vocabulary to lean into.** *Roasted · small-batch · rebel · classic · fire · spark · kick · brew · ride · road · iron · grit · honest · straight-up · hand-packed · fresh · local · the shop · the crew · the roast · the grind (unironically)*

**Vocabulary to avoid.** *Artisanal, curated, bespoke, journey, elevated, experience, unlock, ecosystem, solution, synergize, disrupt.*

**Example copy — approved tone:**

> **EVERY BAG, ROASTED THIS WEEK.**
> No warehouse. No middleman. Beans come in green, leave as coffee — and they don't sit around.

> **THE HOUSE BLEND**
> Dark enough to mean it. Smooth enough to drink all day. Bring the thermos.

> **OPEN 6AM. WE DON'T SLEEP IN.**

**Example copy — off-brand:**

> ~~Discover our curated journey of artisanal single-origin experiences.~~
> ~~Elevate your morning with our hand-crafted bespoke blend. ☕✨~~

---

## VISUAL FOUNDATIONS

### Palette

Two hard anchors and one fire color:

- **Bone / cream paper** (`--paper` `#F6EFE1`) — primary surface. Never pure white; always warm.
- **Tattoo black** (`--ink` `#0E0D0C`) — all strokes, text, hard borders. Never pure `#000`.
- **Candle amber** (`--amber` `#F2A03D`) — primary accent, lifted straight from the candle illustration. Used for flames, highlights, banners, hover glows.

Supporting:
- **Diner rust** (`--rust` `#B4351D`) — CTA buttons, links, "open" signs, errors.
- **Espresso** (`--espresso` `#3A2416`) — photography overlays, deep surfaces.
- **Chrome** (`--chrome-deep` `#8E887D`) — secondary text, rules.
- **Sage** (`--sage` `#5B6B4F`) — laurel leaf, used rarely, mostly in illustration.

Never use: neon gradients, pastel tints, purple, cyan, any "tech blue."

### Typography

| Role | Family | Notes |
|---|---|---|
| Display (signage, billboards) | **Alfa Slab One** 🚩 | Stand-in for a heavy Western slab. Sub for Rosewood, Cooper Black Bold, or a custom woodtype face when available. |
| Heritage serif (editorial H1) | **DM Serif Display** 🚩 | Sub for a Didone / late-19c serif. |
| Industrial sans (H2/H3/UI/body) | **Oswald** 🚩 | Condensed, all-caps friendly, workshop signage energy. |
| Script (flourish, "fresh") | **Yellowtail** 🚩 | Sub for a true brush script; use *sparingly*, one phrase per surface. |
| Typewriter (price tags, captions) | **Special Elite** 🚩 | Like a carbon-paper menu. |

🚩 **All faces are Google-Fonts stand-ins.** If you own licensed faces for any of these roles, drop the files in `fonts/` and swap the `--font-*` vars.

**Rules.**
- Display + heritage serif **never** share a headline. Pick one.
- Don't stack more than 2 families in a single composition (plus script as garnish).
- All-caps ⇒ add `letter-spacing: 0.04–0.08em`. Never tight.
- Body line-height 1.55–1.6. Never below 1.45.

### Backgrounds & texture

- Default background is **warm bone paper** — *never* `#fff`.
- Big marketing surfaces should feel like printed matter: subtle paper grain, occasional halftone dot texture, faded ink registration. A faint noise overlay (`opacity: 0.04–0.08`) is welcome.
- Full-bleed product photography is encouraged (beans, pour shots, the shop, bikes), treated warm — slight amber cast, never cold/blue. B&W with warm sepia wash is also on-brand.
- No gradients except occasional amber → rust radial glows behind the logo or centerpieces. **Absolutely no purple/blue/pink gradients.**
- Repeating patterns: pinstripes, checkerboard (diner tile), halftone dots, stars — all fair game as accents, not wallpaper.

### Borders, rules, shadows

- **Borders are strokes**, 2–3px, solid `--ink`. No 1px hairlines on primary elements.
- Double rules (outer line + inner line, 4–6px gap) on badges, menu panels, printed collateral.
- **Shadows don't blur.** Use **hard offset shadows** (`4px 4px 0 0 var(--ink)`) — stamp-press look, not iOS soft-drop.
- Soft blurred shadows allowed only on floating UI (menus, modals) at low intensity.

### Corner radii

- **Default is square.** `border-radius: 0`.
- Buttons and badges: `2–4px` (still square-looking).
- Pills: full `999px` for tags, chip-style menu items.
- Shield/badge: custom curved shape — reserved for the brand mark only, not decoration.

### Cards

- Bone background, 2px ink border, **hard offset shadow** (`4px 4px 0 var(--ink)`), zero or 2px radius.
- Optional inner double-rule for menu-card treatment.
- No soft floating glass cards. No inner glow.

### Buttons

Three kinds:
- **Primary (rust/ink)** — `--rust` bg, `--paper` text, ink border, hard shadow. Uppercase.
- **Ghost (ink outline)** — transparent, 2px ink border, ink text. Uppercase.
- **Amber callout** — `--amber` bg, ink text, ink border. For "NEW," "LIMITED," "LIVE."

### Hover / press

- **Hover:** `transform: translate(-1px, -1px)` + shadow grows to `5px 5px` — the button "lifts." No color shift required, but amber buttons may darken to `--amber-deep`.
- **Press:** `transform: translate(2px, 2px)` + shadow collapses to `0 0` — the stamp hits the paper. This is the signature interaction.
- **Links:** on hover, text shifts to `--rust-deep`, underline thickens to 3px.

### Animation

- **Sparingly.** This is an analog brand. Nothing floats, bounces, or orbits for no reason.
- Preferred: **stamp-in** (scale 0.96 → 1 with hard settle, `cubic-bezier(.7, 0, .2, 1.4)`, 180ms).
- Fades: 200ms linear; no ease-in-out slop.
- Page transitions: a paper-flip or halftone wipe is on-brand if done well; skip if in doubt.
- Never: parallax scroll trickery, neon glow pulses, scroll-tied particle systems.

### Transparency, blur

- Basically **none** for primary UI. This is a matte, opaque, ink-on-paper brand.
- Exception: overlay on dark photography can be `rgba(14,13,12,0.55)` with no blur.

### Layout rules

- Wide gutters, not edge-to-edge. Think newspaper margins.
- Strong grid, but elements can **tilt** (-2° to -6°) for banner/sticker/stamp placements. Tilts are intentional, not random.
- Sticker/stamp pattern: rotate slightly, use hard shadow, overlap other elements — feels pasted on.

---

## ICONOGRAPHY

**No icon font is used.** The brand has no codebase icon set to inherit. Iconography is kept minimal and hand-drawn in feel.

**System in use:**
- **Lucide** (via CDN) 🚩 — used for functional UI icons (nav, form affordances, menus, close/check). 2px stroke matches the ink-on-paper weight well. Substitution — flag if the user owns a licensed hand-drawn set.
- **Brand illustrations** (in `assets/`) — the double-candle, the shield, the badge — used at size for marketing/hero moments, never shrunk to UI icon scale.
- **Unicode dingbats** — `★ ✦ ✧ ❖ ◆` — for decorative dividers in menus and lists.

**Rules.**
- **No emoji.** Ever.
- Lucide icons render at `stroke-width: 2px`, `color: currentColor`. Size 16/20/24.
- Brand illustrations are never recolored away from the palette.
- Never hand-draw SVG to mimic the candle/shield — always use the provided PNG/PDF assets.

Assets available:
- `assets/logo-shield.png` — primary crest
- `assets/badge-black.jpg`, `assets/badge-white.png` — 2026 badge (light & dark use)
- `assets/double-candle.png`, `assets/double-candle.pdf` — illustration

---

## Index

Root files:
- **`README.md`** — this file
- **`colors_and_type.css`** — CSS custom properties for color, type, spacing, radii, shadows
- **`SKILL.md`** — portable Agent Skill manifest
- **`assets/`** — logos, badges, illustrations
- **`fonts/`** — (empty; Google Fonts used via `@import` in `colors_and_type.css` 🚩)
- **`preview/`** — Design-System-tab preview cards
- **`ui_kits/website/`** — marketing site UI kit (index.html + components)

UI kits:
- **Website / marketing** — `ui_kits/website/index.html` (Header, Hero, Marquee, Coffees grid, Story, MenuBoard, Visit, Footer + shared Primitives)

Slides: *none* — no deck template was provided, so no sample slides were generated.

---

## Caveats & open questions (please confirm)

1. 🚩 **All fonts are Google Font substitutions.** If the brand owns licensed faces (Rosewood, Cooper Black, a custom woodtype, a real brush script, etc.) drop them in `fonts/` and I'll rewire.
2. 🚩 **Palette extrapolated** from the candle illustration's amber + the shield's black-on-bone. If the brand uses specific Pantones for print, swap the hex values in `colors_and_type.css`.
3. 🚩 **No product screenshots, no website, no codebase.** The website UI kit is a hi-fi *proposal* of what a Rockabilly Roasting marketing site could look like given the brand — not a recreation of an existing one. If there *is* an existing site, please share it and I'll rebuild the kit against reality.
4. 🚩 **Icon system** defaults to Lucide via CDN. If the brand has a preferred hand-drawn set, please share.
5. 🚩 **Content copy** in the kit is invented on-voice. Replace with real menu/shop copy when available.
