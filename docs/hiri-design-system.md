# Hiri — UI Design System

One reference document covering brand identity, visual language, component architecture, and surface structure. When making any UI decision, this is the first and only document to consult.

---

## Design Philosophy

Hiri's interface is **pragmatic craft** — a reliable tool that respects both the user's time and their trade. The UI stays out of the way, communicates clearly, and carries subtle warmth that acknowledges the human scale of small-batch roasting.

This is software for busy people who care about quality. It should feel capable without being corporate, warm without being cute.

### Principles

**Respect the operator.** Users are often mid-task — between roasts, packing orders, answering wholesale inquiries. The interface should support quick, confident action. Don't make them hunt for things or confirm the obvious.

**Clarity over cleverness.** Every label, message, and interaction should be immediately understood. Avoid jargon, abbreviations, and ambiguous icons. When in doubt, use words.

**Calm confidence.** The UI should feel steady and trustworthy. No unnecessary animations, no excitement where none is warranted. When something succeeds, confirm it simply. When something fails, explain it plainly.

**Warmth in the details.** Small touches — a helpful empty state, a well-written confirmation, comfortable spacing — add up to an interface that feels human. This warmth is subtle, never performative.

**Density with breathing room.** Show enough information to be useful without overwhelming. Tables and lists should be scannable. Forms should not feel cramped. Whitespace is a feature.

---

## Brand Heritage

The Hiri name comes from the traditional trade voyages of the Motu people of Papua New Guinea — purposeful journeys of exchange between communities. The UI doesn't need to be overtly themed around this heritage, but the visual language carries subtle echoes of the Hiri Moale festival and its traditions.

The palette draws from festival imagery: the coastal teal of Gulf waters, the sunset amber of the lakatoi returning at dusk, the deep brown-black of trading canoe hulls, the warm off-white of kina shells. These appear in muted, professional form.

The logo mark references the distinctive double crab-claw sails of lakatoi trading canoes.

**Avoid:**
- Literal Pacific imagery (canoes, waves, tribal patterns) in the UI
- Tiki or "island" aesthetic
- Anything that feels like cultural costume or tourism
- Overexplaining the name's origin in the interface

The brand is named Hiri. The UI is a professional tool for coffee businesses. The heritage gives the name meaning and warmth without dictating the visual design.

---

## Visual Language

### Color Palette

The palette has eleven colors. This is the complete set — no additions without a documented reason.

#### Tailwind config extensions

Three brand colors require custom tokens in `tailwind.config.js`:

```javascript
// tailwind.config.js
module.exports = {
  theme: {
    extend: {
      colors: {
        'hiri-teal': {
          DEFAULT: '#2D7A7A',  // primary action, links, focus
          dark:    '#1E5E5E',  // hover state
        },
        'hiri-amber': '#C4873A', // secondary accent + warning (same token)
        'hiri-red':   '#B85C4A', // errors, destructive actions
      },
    },
  },
}
```

All other colors use Tailwind built-ins.

#### Full palette reference

**Base colors — used most frequently:**

| Role | Color | Tailwind class |
|---|---|---|
| Page background | `#FAF9F7` warm off-white | `bg-[#FAF9F7]` or extend as `bg-warm-white` |
| Surface (cards, panels, inputs) | `#FFFFFF` | `bg-white` |
| Border | `#E5E2DE` warm gray | `border-[#E5E2DE]` or extend as `border-warm` |
| Text primary | `#1C1917` deep brown-black | `text-stone-950` |
| Text secondary | `#78716C` warm gray | `text-stone-500` |

**Accent colors — used sparingly, purposefully:**

| Role | Color | Tailwind class |
|---|---|---|
| Primary action (buttons, links, focus) | `#2D7A7A` Gulf teal | `bg-hiri-teal` / `text-hiri-teal` |
| Primary hover | `#1E5E5E` deep teal | `hover:bg-hiri-teal-dark` |
| Secondary accent (highlights, badges) | `#C4873A` sunset amber | `bg-hiri-amber` / `text-hiri-amber` |

**Semantic colors — appear only when conveying status:**

| Role | Color | Tailwind class |
|---|---|---|
| Success | `#3D8B6E` coastal green | `text-emerald-700` / `bg-emerald-50` |
| Warning | `#C4873A` trade amber | `text-hiri-amber` / `bg-amber-50` |
| Error | `#B85C4A` ochre red | `text-hiri-red` / `bg-red-50` |
| Info | `#4A7FA8` water blue | `text-blue-600` / `bg-blue-50` |

Note: warning and secondary accent share `hiri-amber`. One token, two roles — intentional.

**Usage rules:**
- The interface should feel predominantly neutral. Color is used purposefully, not decoratively.
- `hiri-teal` is reserved for interactive elements and focus states only.
- `hiri-amber` as accent appears on badges, highlights, and key status indicators — not on every interactive element.
- Semantic colors appear only when conveying status or feedback — never for decoration.
- Never use multiple accent colors in close proximity.
- The palette should feel warm and grounded, never cold or stark.

### Typography

**Font stack:** System sans-serif — no web font loading on the critical path.

```css
font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
```

**Exception:** The Hiri wordmark uses Outfit Semibold (Google Fonts). Load this only where the logo is rendered, not as a body font.

#### Type scale — Tailwind standard classes

| Role | Tailwind class | Size | Weight | Usage |
|---|---|---|---|---|
| Page title | `text-2xl font-semibold` | 24px | 600 | Main page headings |
| Section title | `text-lg font-semibold` | 18px | 600 | Card headings, section breaks |
| Body | `text-base font-normal` | 16px | 400 | Default text, table cells, form values |
| Label | `text-sm font-medium` | 14px | 500 | Form labels, table headers, nav items |
| Small | `text-sm font-normal` | 14px | 400 | Helper text, timestamps, metadata |
| Badge / status | `text-xs font-medium` | 12px | 500 | Badges, status indicators |

**Line height:** `leading-normal` (1.5) for body text, `leading-tight` (1.25) for headings.

**Guidelines:**
- Don't use bold for emphasis in running text — use it for labels and headings only.
- Don't use all-caps except for badges and status indicators.
- Text secondary (`text-stone-500`) for labels, helper text, timestamps, and metadata.
- Text primary (`text-stone-950`) for all content the user needs to read and act on.

### Spacing

4px base unit via Tailwind spacing scale. All spacing uses multiples of this unit — no arbitrary values.

| Token | Value | Tailwind | Usage |
|---|---|---|---|
| xs | 4px | `p-1` / `gap-1` | Tight spacing, icon padding |
| sm | 8px | `p-2` / `gap-2` | Related elements, input padding |
| md | 16px | `p-4` / `gap-4` | Standard gaps, section padding |
| lg | 24px | `p-6` / `gap-6` | Card padding, major sections |
| xl | 32px | `p-8` / `gap-8` | Page margins, section breaks |
| 2xl | 48px | `p-12` / `gap-12` | Major page sections |

Be generous with padding inside cards and panels. Group related items closely; separate unrelated items clearly.

### Border Radius

| Element | Radius | Tailwind |
|---|---|---|
| Buttons | 6px | `rounded-md` |
| Inputs | 6px | `rounded-md` |
| Cards | 8px | `rounded-lg` |
| Badges | 4px | `rounded` |
| Modals | 12px | `rounded-xl` |

Avoid pill shapes (`rounded-full`) except for small status dots. The interface should feel grounded, not bubbly.

### Shadows

Use shadows sparingly. Most elements rely on borders and background contrast for definition.

| Usage | Shadow | Tailwind |
|---|---|---|
| Cards (subtle lift) | `0 1px 3px rgba(0,0,0,0.06)` | `shadow-sm` |
| Dropdowns, modals | `0 4px 12px rgba(0,0,0,0.10)` | `shadow-lg` |
| Hover states | `0 2px 6px rgba(0,0,0,0.08)` | `shadow-md` |

Two elevation levels in practice: `shadow-sm` for resting surfaces, `shadow-lg` for floating elements (modals, dropdowns, toasts). `shadow-md` only on hover — never as a default state.

---

## Logo

The logo combines a stylized double crab-claw sail mark with the Hiri wordmark, referencing the distinctive sails of lakatoi trading canoes.

**Mark:** Two overlapping sail shapes. The back sail renders at 35% opacity to create depth.

**Wordmark:** "hiri" in lowercase Outfit Semibold, letter-spacing `-0.01em`.

| Variant | Usage |
|---|---|
| Primary | Teal mark + dark wordmark on light background |
| Dark mode | Off-white mark + wordmark on dark background |
| Brand background | Off-white mark + wordmark on teal background |
| Monochrome | Single color for both elements |

**App icon:** Sails + wordmark on teal rounded square. At sizes below 32px, use sails-only version.

**Clear space:** Maintain padding equal to the height of the "h" in the wordmark on all sides.

**Never:** stretch, rotate, alter proportions, change sail opacity relationship, add effects, use colors outside the defined palette.

See `planning/LOGO.html` for SVG source.

---

## Components

### Tier 1 — Primitives

Atoms with no business knowledge. Used across all four surfaces.

**Button**

| Variant | Background | Border | Text | Usage |
|---|---|---|---|---|
| Primary | `bg-hiri-teal` | none | white | Main action per section. One per view. |
| Secondary | `bg-white` | `border-[#E5E2DE]` | `text-stone-700` | Secondary actions, cancel, back |
| Danger | `bg-white` → `bg-hiri-red` on confirm | `border-hiri-red` | `text-hiri-red` | Destructive actions |
| Ghost | transparent | none | `text-hiri-teal` or `text-stone-500` | Tertiary actions, table inline actions |

| Size | Padding | Tailwind | Usage |
|---|---|---|---|
| sm | 6px 12px | `px-3 py-1.5 text-sm` | Inline actions, table rows |
| md | 10px 16px | `px-4 py-2.5 text-sm` | Default, form submissions |
| lg | 12px 20px | `px-5 py-3 text-base` | Primary CTAs |

All buttons: `rounded-md font-medium transition-colors duration-150`

Loading state: spinner replaces button text, button disabled. Every button that triggers an HTTP request shows a loading state between click and response — use `hx-indicator` with htmx.

**Input**

```
Border:        border border-[#E5E2DE] rounded-md
Padding:       px-3 py-2.5
Focus:         focus:outline-none focus:ring-2 focus:ring-hiri-teal focus:border-hiri-teal
Error state:   border-hiri-red focus:ring-hiri-red
Disabled:      bg-stone-50 text-stone-400 cursor-not-allowed
```

Labels always above the input, never as placeholder-only. Helper text below label when needed (`text-sm text-stone-500`). Error message below input when invalid (`text-sm text-hiri-red`).

**Badge**

`text-xs font-medium rounded px-2 py-0.5`

| Status | Classes |
|---|---|
| Active / Success | `bg-emerald-50 text-emerald-700` |
| Pending / Warning | `bg-amber-50 text-hiri-amber` |
| Error / Overdue | `bg-red-50 text-hiri-red` |
| Neutral / Info | `bg-stone-100 text-stone-600` |

**Spinner**

Animated SVG. Appears inside buttons (loading state), inline next to loading text, and as a page-section loader. Never as a full-page spinner — use skeleton screens for initial page loads.

**Empty state**

Helpful, not sad. No emoji.

```
[Optional subtle illustration]
Brief statement of what's empty       — text-base text-stone-950
Guidance on what to do next           — text-sm text-stone-500
[Action button — when applicable]
```

Example: "No products yet. Products you create will appear here. [Add Product]"

### Tier 2 — Compositions

Built from primitives. Reusable across surfaces.

**FormField** — Label + Input + helper text + FieldError composed together. The standard unit of form building.

**Card** — `bg-white rounded-lg shadow-sm border border-[#E5E2DE] p-6`. Use to group related content. Don't nest cards within cards.

**DataTable** — Header row (`bg-[#F8F7F6]`, label typography), body rows (white, `border-b border-[#E5E2DE]`), row hover (`bg-[#FAFAF8]`), cell padding (`px-4 py-2.5`). Right-align numeric columns. Always include an empty state — never just a blank table. Pagination below.

**PageHeader** — Page title (text-2xl) top-left + optional description (text-sm text-stone-500) + optional primary action top-right.

**SectionHeader** — Within-page section heading (text-lg font-semibold) with optional secondary action.

**Pagination** — Prev/next + page numbers. Label: "Showing 1–20 of 84".

**StepIndicator** — Multi-step progress for checkout and subscription setup.
- Desktop: `[✓ Cart] → [✓ Shipping] → [● Payment] → [  Confirm]`
- Mobile: `Payment · Step 3 of 4`
- Completed steps: checkmark, clickable (user can go back)
- Current step: `text-hiri-teal font-medium`
- Future steps: `text-stone-400`

**OrderSummary** — Line items + totals. Reused in checkout and order history. Always shows tax and shipping lines even when zero — never omit the line.

**AddressCard** — Formatted address + optional edit/delete actions.

**SubscriptionCard** — Subscription summary + status badge + action buttons (pause, skip, cancel).

**FlashBanner** — Dismissible full-width message below nav, above content. For page-level feedback that isn't a toast.

### Tier 3 — Page sections

Large compositions specific to one page. Defined inline in the page's templ file (lowercase, private). When a page section is needed on a second page, it is promoted to Tier 2 and moved to `ui/components/`.

---

## Feedback Patterns

Four patterns. Each has a defined purpose. Never mix them arbitrarily.

### 1. Inline validation

**Purpose:** field-level errors.

**Rules:**
- Appears on blur — not on keystroke. Don't punish the user mid-typing.
- Error clears on keystroke after correction — give positive feedback immediately.
- Error text below the field: `text-sm text-hiri-red` with a small exclamation icon.
- Field border changes to `border-hiri-red` on error.
- One error per field at a time — show the most actionable error first.
- The message explains what is wrong AND what to do:
  - ❌ "Invalid email"
  - ✅ "Enter a valid email address (e.g. name@example.com)"

Alpine drives show/hide. The field error component is a shared Tier 1 primitive.

### 2. Toast notifications

**Purpose:** confirmation that an action succeeded, or a non-blocking background error.

**Not for:** form validation errors — those are inline.

**Rules:**
- Position: fixed bottom-right on desktop, bottom-center on mobile.
- Auto-dismiss: 4 seconds for success, 6 seconds for errors.
- Maximum 3 visible at once — older ones push up and fade.
- Always include a manual dismiss button.
- Success: `border-l-4 border-emerald-600` with checkmark icon.
- Error: `border-l-4 border-hiri-red` with exclamation icon.
- Error toasts for unresolvable problems name the issue and offer a path forward:
  - ❌ "An error occurred"
  - ✅ "Subscription couldn't be paused. Try again or contact support."

Server-side actions trigger toasts via `HX-Trigger` response header:

```go
// web/respond.go
func TriggerToast(w http.ResponseWriter, kind, message string) {
    payload := fmt.Sprintf(`{"showToast":{"type":%q,"message":%q}}`, kind, message)
    w.Header().Set("HX-Trigger", payload)
}
```

### 3. Inline confirmation dialogs

**Purpose:** gate destructive or irreversible actions.

**Not for:** adding to cart, placing an order, changing an address — these are not destructive.

**Rules:**
- Appears inline near the triggering element — not a centered modal overlay.
- Confirm button text describes the action: "Cancel subscription", not "Confirm" or "OK".
- Cancel dismisses with no state change.
- Destructive confirm button: `bg-hiri-red text-white`.
- Used for: cancel subscription, delete address, deactivate product, remove discount.

Alpine pattern:
```html
<div x-data="{ confirming: false }">
    <button x-show="!confirming" @click="confirming = true"
        class="text-sm text-hiri-red hover:text-hiri-red/80">
        Cancel subscription
    </button>
    <div x-show="confirming" class="flex items-center gap-3">
        <span class="text-sm text-stone-600">Cancel your subscription?</span>
        <button hx-post="/subscriptions/{{ .ID }}/cancel"
                hx-target="#subscription-{{ .ID }}" hx-swap="outerHTML"
                class="rounded-md bg-hiri-red px-3 py-1.5 text-sm text-white">
            Cancel subscription
        </button>
        <button @click="confirming = false"
                class="text-sm text-stone-500 hover:text-stone-700">
            Keep it
        </button>
    </div>
</div>
```

### 4. Progress indicators

**Purpose:** orientation in multi-step flows (checkout, subscription setup).

**Rules:**
- Show step names, not numbers alone: "Shipping" not "Step 2".
- Completed steps are checkmarked and clickable — the user can go back.
- Progress is never lost on back navigation.
- On mobile, collapse to current step name + "Step N of N".

---

## Error Message Philosophy

### The three categories

**Recoverable — user can fix it.** Name the problem. Tell them exactly what to do.
- ❌ "Invalid email address"
- ✅ "Enter a valid email address (e.g. name@example.com)"

**Recoverable — system will retry.** Tell them what happened and what's happening next.
- ❌ "Payment failed"
- ✅ "Your payment didn't go through. We're retrying — this usually resolves in a moment."

**Unrecoverable.** Name the problem. Confirm no charge. Give them a path forward.
- ❌ "Something went wrong. Please try again."
- ✅ "We couldn't process your order right now. Your card has not been charged. Try again in a few minutes or contact us at support@hiri.com."

### Rules

1. Always name the problem. "Something went wrong" is not a problem name.
2. Never blame the user for system errors.
3. Always offer a next step — even if it's "contact support."
4. Don't hide errors. Silent failures erode trust faster than visible ones.
5. Match severity to presentation — see matrix below.

### Severity matrix

| Error type | Presentation | Persists |
|---|---|---|
| Field validation | Inline below field | Until corrected |
| Form submission failure (user fixable) | Inline banner above submit button | Until corrected |
| Action success | Success toast | Auto-dismiss 4s |
| Action failure (retryable) | Error toast with retry suggestion | Until dismissed |
| Action failure (unrecoverable) | Error toast with contact path | Until dismissed |
| Page-level failure (404, 403, 500) | Full error page with navigation | N/A |

---

## Voice and Tone

### Guidelines

- Be direct and concise.
- Use plain language.
- Address the user as "you" when needed.
- Avoid exclamation points except in genuinely celebratory moments.
- Don't anthropomorphize the software ("We're working on it!").
- Page titles and navigation: use nouns ("Orders", not "Manage Orders").
- Button labels: use clear verbs ("Save", "Create", "Delete"). Not vague ones ("Submit", "OK").
- Confirmations: state what happened ("Order marked as shipped"), include next step when relevant.

### Examples

| Context | ❌ Avoid | ✅ Use |
|---|---|---|
| Product created | "Awesome! Your product is ready!" | "Product created" |
| Empty orders | "No orders yet 😢" | "No orders yet" |
| Form error | "Oops! Something went wrong" | "Couldn't save. Price is required." |
| Delete confirm | "Are you sure?" | "Delete Mountain Blend? This can't be undone." |
| Loading | "Hang tight..." | (spinner, no text) |
| Success toast | "Changes saved successfully!" | "Changes saved" |
| Subscription cancelled | "Your subscription has been cancelled! See you next time 👋" | "Subscription cancelled. You can reactivate at any time." |

---

## The Four Surfaces

One design system, four surfaces. The palette, type scale, and component library are shared. Layout density and device priority differ.

| Surface | Users | Device priority | Density |
|---|---|---|---|
| **Storefront + Account** | Retail customers | Mobile first | Low — generous spacing, large tap targets |
| **Wholesale portal** | B2B buyers | Desktop first | Medium — efficient data entry, repeat orders |
| **Customer account panel** | Authenticated customers | Mobile + desktop | Medium |
| **Staff admin** | Staff and operators | Desktop first | High — data tables, action-heavy |

Surface-specific layout patterns, navigation structures, and page-by-page design are documented in `lean-commerce-ui-plan.md`. This document defines the shared language those surfaces are built from.

---

## Accessibility

- Color contrast: minimum 4.5:1 for body text, 3:1 for large text.
- Focus indicators: visible ring (`focus:ring-2 focus:ring-hiri-teal`) on all interactive elements.
- Labels: all form inputs have associated labels — not placeholder-only.
- Don't rely on color alone to convey meaning — pair with icons or text.
- Touch targets: minimum 44×44px on mobile.
- Use semantic HTML: buttons for actions, links for navigation.
- Alt text on all meaningful images.

---

## Responsive Behavior

| Breakpoint | Width | Tailwind prefix | Target |
|---|---|---|---|
| Mobile | < 640px | (default) | Phones |
| Tablet | 640–1024px | `sm:` | Tablets, small laptops |
| Desktop | > 1024px | `lg:` | Laptops, desktops |

**Navigation:**
- Desktop: persistent sidebar (admin, wholesale) or top nav (storefront)
- Mobile: collapsible hamburger menu or bottom tab bar (account panel)

**Tables:**
- Desktop: full table with all columns
- Mobile: card-based list — each row becomes a card with the most important fields visible and secondary fields collapsed

**Forms:**
- Desktop: two-column layout where fields are logically paired
- Mobile: single column, full-width inputs, minimum 44px tap targets

**Page actions:**
- Desktop: top-right of page header
- Mobile: sticky bottom bar or inline below content

---

## Reference Interfaces

| Product | What to reference |
|---|---|
| Linear | Information density, calm palette, keyboard-first feel |
| Stripe Dashboard | Table design, clear hierarchy, professional warmth |
| Notion | Clean typography, comfortable spacing, understated |
| Basecamp | Friendly but professional tone, opinionated simplicity |

---

## Tailwind Configuration Summary

```javascript
// tailwind.config.js
module.exports = {
  content: [
    './internal/ui/**/*.templ',
    './ui/checkout/**/*.svelte',
  ],
  theme: {
    extend: {
      colors: {
        'hiri-teal': {
          DEFAULT: '#2D7A7A',
          dark:    '#1E5E5E',
        },
        'hiri-amber': '#C4873A',
        'hiri-red':   '#B85C4A',
      },
      fontFamily: {
        sans: [
          '-apple-system', 'BlinkMacSystemFont', '"Segoe UI"',
          'Roboto', 'Helvetica', 'Arial', 'sans-serif',
        ],
      },
      // Background for page — warm off-white
      backgroundColor: {
        'warm-white': '#FAF9F7',
      },
      borderColor: {
        'warm': '#E5E2DE',
      },
    },
  },
  plugins: [],
}
```

---

## What this document does not cover

- **Surface-by-surface page design** — covered in `lean-commerce-ui-plan.md`
- **templ file organization** — covered in `lean-commerce-package-structure.md`
- **Svelte checkout component internals** — covered in `lean-commerce-package-structure.md`
- **Email templates** — separate concern, not part of the templ component system
- **Dark mode** — not in scope; this is a light-mode-first system