# Admin UI conventions

The admin panel (Hiri) is a working tool for staff, not a brand showcase. It shares the storefront's underlying palette but uses a deliberately quieter visual language — see *Admin Panel (Hiri)* in `CLAUDE.md` for the high-level brief.

This doc is the **enforceable** companion to that brief: what classes to reach for, what classes to never reach for, and how the lint catches violations.

For how a `/admin/<thing>/{id}` page is *structured* — main column vs rail, where each class of action lives, activity timelines, and the traps that bite when one of these pages grows — see `docs/admin-detail-pages.md`.

> **Scope.** This applies to every templ file under `internal/ui/admin/` *except* `staff_login.templ`, which is a standalone branded splash page using its own layout and font import — it's intentionally paper-and-ink. The lint skips it.

---

## Use these

### Surfaces and text

| Token | Hex (admin scope) | Use for |
| --- | --- | --- |
| `bg-rr-bg` | `#F6EFE1` | Page background — kraft paper |
| `bg-rr-surface` | `#FFFBF1` | Cards, panels, elevated surfaces — cream-hi, never `#FFFFFF` |
| `bg-rr-raised` | `#ECE0C6` | Hover rows, active nav bg, subtle emphasis |
| `text-rr-heading` | `#0E0D0C` | Headings, primary text, active nav |
| `text-rr-body` | `#1A1A1A` | Body copy |
| `text-rr-muted` | `#6B6862` | Captions, labels, secondary info |
| `border-rr-border` | `#DCD1B8` | All borders — always 1px |

Surfaces are warm, and each step has to be visible against the **next one out**.
The content area never paints `bg-rr-bg` — the layout sets kraft paper inline on
`<html>`, so paper is what "raised" elements actually sit on. Raised and border
were previously cool greys (`#F0EEE8` / `#E5E2DA`) chosen against a `#FAFAF6`
page that never renders; on paper that was a ~2% step and it disappeared. If you
add a new surface token, check it against `#F6EFE1`, not against white.

### Accents (use sparingly)

| Token | Hex | Use for |
| --- | --- | --- |
| `bg-rr-red` / `text-rr-red` | `#B4351D` | Primary CTAs only. Stale-row indicators (`row-link-stale`). Avatar circles. **Never chrome.** |
| `text-rr-amber` / `bg-rr-amber` | `#F2A03D` | Row-hover inset bar, "needs attention" inline affordances. |

### Typography

| Class | Family | Use for |
| --- | --- | --- |
| (default, no class) | Inter | All body, UI, table cells, form fields. |
| `admin-page-title` | Alfa Slab One | Page-level `<h1>` only. Sole brand touch in admin chrome. |
| `label-font` | Inter (overridden in admin scope) | Small uppercase tracked labels — group headings, table label cells. |

### Status badges

Use the existing semantic palette in `admin.templ`'s style block — admin needs more colors than the locked storefront palette because staff scan dozens of rows at once. Apply via the `badge` base class plus a modifier:

`badge-green`, `badge-green-solid`, `badge-amber`, `badge-slate`, `badge-teal`, `badge-teal-solid`, `badge-red`, `badge-grey`, `badge-pastdue`, `badge-partial`, `badge-blue`, `badge-indigo`, `badge-neutral`

### Buttons

| Class | Use for |
| --- | --- |
| `btn-secondary` | Default non-destructive action |
| `btn-confirm` | Confirm/save (green tint) |
| `btn-danger` | Destructive action (red tint) |
| Inline `bg-rr-red text-white …` | Primary CTAs — keep it to one per view |

### Rows and tables

| Class | Use for |
| --- | --- |
| `row-link` | Clickable table rows — gets amber inset bar on hover |
| `row-link-stale` | Persistent rust flag on a row that's waiting/stale |
| `row-link-target` | The `<a>` inside that owns the full-row click area |

### Section navigation

| Component | Use for |
| --- | --- |
| `adminSectionTabs` | Sibling pages inside one section (Catalog, Settings). Renders above the page `<h1>`. A tab with a non-zero `Count` gets an amber badge; zero renders nothing, because a "0" reads as a number to check rather than as nothing to do. |
| `adminChannelToggle` / `adminChannelToggleCount` | The same list scoped by sales channel (Retail / Wholesale). |

Settings adds a second piece on top of the strip: a **Needs attention** list (`settingsAttention`) that renders on every page in the section, so a misconfiguration cannot hide behind a tab nobody clicked. Rows use `row-link` + `row-link-stale` — the same rust flag the order lists use for stuck work. It renders nothing when there is nothing wrong; an always-present "all clear" panel trains staff to skip the spot where the real warning will appear.

### Shadows

Use 1px hairlines on cards (`border border-rr-border`) and `shadow-sm` if you need slight elevation. Never use the storefront stamp shadows (`shadow-stamp*`).

---

## Don't use these in admin

These classes exist for the storefront/marketing surfaces. Reaching for them in admin breaks the warm-professional treatment — and `mage check` will refuse it.

### Banned colors (direct paper-and-ink utilities)

`bg-paper`, `bg-paper-warm`, `bg-paper-deep`, `bg-cream-hi`, `bg-ink`, `bg-ink-soft`, `bg-rust`, `bg-rust-deep`, `bg-candle`, `bg-candle-deep`, `bg-candle-soft`, `bg-espresso`, `bg-espresso-deep`, `bg-chrome`, `bg-chrome-deep`, `bg-sage`

(and the `text-*` and `border-*` versions of all of the above)

**Why:** Admin uses the `rr-*` token layer, which is overridden inside the admin scope to point at the warm-professional palette. Direct paper-and-ink utilities bypass that override and lock you into storefront colors.

### Banned fonts

`font-slab`, `font-heritage`, `font-script`, `font-special`, `font-oswald`

**Why:** Admin runs on Inter (body) and Alfa Slab One (page titles via `admin-page-title`). Storefront display/script/typewriter families are not loaded in admin scope and don't belong here.

### Banned storefront behaviors

`btn-stamp`, `btn-stamp-paper`, `shadow-stamp`, `shadow-stamp-sm`, `shadow-stamp-lg`, `shadow-stamp-paper`, `texture-halftone-paper`, `texture-dots`, `texture-grid`, `flame-stripe`, `candle-flicker`, `marquee-inner`, `window-glow`, `string-lights`, `product-card`, `nav-link`, `roast-dot`, `cart-checkmark`

**Why:** These are marketing flourishes — stamp shadows, flame dividers, candle flickers, marquees, product card hover lifts. The admin's motion is utility, not theatre.

### Banned border weight

`border-2`, `border-t-2`, `border-r-2`, `border-b-2`, `border-l-2` (and `border-4`, etc.)

**Why:** Admin uses 1px hairlines. Heavy borders are storefront stamp aesthetic. If you need stronger visual separation, use a row background change or a `bg-rr-raised` block, not a thick border.

---

## Lint enforcement

`mage checkAdminUI` (and `mage check` which calls it) greps
`internal/ui/admin/**/*.templ` **and `internal/ui/layouts/**/*.templ`** — the
admin shell is admin UI too — for the deny-list above, and fails if any matches
are found. `staff_login.templ`, `staff_setup.templ` and `layouts/storefront.templ`
are excluded; the last of those is *meant* to use the paper-and-ink classes.
`<style>` blocks are skipped, because a token named inside the shell's own
override stylesheet is a definition rather than a class somebody reached for.

That is one half of the check. The other is described under *The lint has two
halves* below.

To add a new admin-allowed token or update the deny-list, edit the lint target in `magefiles/mage.go`.

---

## When you genuinely need a marketing-style element in admin

You probably don't. If you think you do, ask first — there's almost always an admin-tokens equivalent. Cases that *seem* to need storefront classes and what to use instead:

| You're tempted to use | Use instead |
| --- | --- |
| `bg-ink text-paper` for a "selected" pill | `bg-rr-heading text-rr-bg` |
| `font-oswald` for an uppercase label | `label-font` (already aliased to Inter in admin) |
| `shadow-stamp-sm` for emphasis on a card | `border border-rr-border` + `shadow-sm` |
| `flame-stripe` as a section divider | `border-t border-rr-border` |
| `font-slab` for a heading | `admin-page-title` (Alfa Slab One, page-level only) or just `font-semibold text-rr-heading` |

If you're building something genuinely new (e.g., an admin onboarding splash), open a discussion before reaching for storefront classes — we may want to expand the admin palette explicitly.

## The lint has two halves

`mage checkAdminUI` (wired into `mage check`) fails on two things:

1. **A banned class** — the storefront paper-and-ink utilities, brand fonts,
   stamp shadows and heavy borders listed above. A class that is *wrong*.
2. **A class that emits no CSS** — an `rr-*` utility Tailwind never produced a
   rule for. A class that does not *exist*.

   Scope is `internal/ui/admin` plus `internal/ui/layouts` (the admin shell),
   and it matches `rr-*` utilities written literally in the templates. Known
   gaps, deliberately: a class assembled by string concatenation, a *non*-`rr`
   typo like `bg-surface`, and `rr-*` classes written in Go or JS rather than a
   `.templ`. This closes the hole that shipped, not every hole.

The second half was added after `bg-rr-paper-warm` shipped. The token is
`--color-paper-warm`: un-prefixed, and storefront-only. Tailwind v4 drops an
unknown utility silently, so it compiled, passed the blocklist, passed the
render tests — and produced no CSS at all. Today was simply not marked on the
maintenance calendar, and nothing short of opening a browser could have said so.

If you add a token, add it to `input.css`; if the lint calls a class dead, the
name is wrong rather than the check.

**It is a local gate, not a CI one.** Nothing in `.github/workflows` runs `mage
check` — the deploy workflows build and ship. So the lint only catches what
somebody runs it against before pushing, which is how `bg-rr-paper-warm` reached
`main`'s doorstep in the first place. Wiring `mage check` into CI is the obvious
follow-up and is deliberately not done here, because it belongs to the repo's
build setup rather than to a feature branch.
