---
name: rockabilly-roasting-design
description: Use this skill to generate well-branded interfaces and assets for Rockabilly Roasting Co., either for production or throwaway prototypes/mocks/etc. Contains essential design guidelines, colors, type, fonts, assets, and UI kit components for prototyping.
user-invocable: true
---

Read the `README.md` file within this skill, and explore the other available files.

If creating visual artifacts (slides, mocks, throwaway prototypes, etc), copy assets out and create static HTML files for the user to view. If working on production code, you can copy assets and read the rules here to become an expert in designing with this brand.

If the user invokes this skill without any other guidance, ask them what they want to build or design, ask some questions, and act as an expert designer who outputs HTML artifacts _or_ production code, depending on the need.

## What's inside

- `README.md` — brand context, voice & content, visual foundations, iconography, caveats
- `colors_and_type.css` — CSS custom properties for the full color + type + spacing + shadow system
- `assets/` — logos (shield + 2026 badge, light & dark), double-candle illustration
- `preview/` — static cards used by the Design System tab; safe to reference for examples
- `ui_kits/website/` — React/JSX marketing site kit: Header, Hero, Marquee, Coffees, Story, MenuBoard, Visit, Footer + shared Primitives

## Quick rules (non-negotiable)

- Paper, not white. Ink, not black. Never pure `#fff` or `#000`.
- Hard offset shadows (`4px 4px 0 var(--ink)`), never soft drop shadows on primary elements.
- All-caps headlines in Oswald/Alfa Slab; tight tracking is wrong here (0.04–0.08em minimum).
- No emoji, ever. Use unicode stars/dingbats or brand illustrations instead.
- Voice: confident, warm, plainspoken. No "artisanal," "curated," "journey," etc.
- Fonts are Google Fonts substitutions — flag this if the user is doing final/print work.
