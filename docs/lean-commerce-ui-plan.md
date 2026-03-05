# Lean Commerce — UI Plan

Four surfaces, one design system, Clean Utility aesthetic. This document covers surface structure, component architecture, feedback patterns, and templ organization. It is the frontend equivalent of the package structure document — it defines the rules before the code is written.

---

## The four surfaces

| Surface | Primary users | Primary device | Density | Auth |
|---|---|---|---|---|
| **Storefront + Account** | Retail customers | Mobile | Low — generous spacing, large targets | Optional (guest) → Required (account) |
| **Wholesale portal** | B2B buyers | Desktop | Medium — efficient data entry, repeat orders | Required |
| **Customer admin** | Any authenticated customer | Mobile + Desktop | Medium | Required |
| **Staff admin** | Staff and operators | Desktop | High — data tables, action-heavy | Required (staff session) |

Storefront and customer account share a layout and nav — a logged-in retail customer sees the same top nav with account links appearing after login. The wholesale portal is a separate surface with its own layout, separate login, and different nav. Staff admin is completely separate.

---

## Aesthetic system — Clean Utility

One design language that scales from a product card to a data table without fighting itself.

### Color palette

```
Background:   zinc-50   (#fafafa)   — page background
Surface:      white     (#ffffff)   — cards, panels, inputs
Border:       zinc-200  (#e4e7eb)   — all borders, dividers
Text primary: zinc-900  (#18181b)   — headings, body
Text muted:   zinc-500  (#71717a)   — labels, captions, placeholders
Accent:       amber-600 (#d97706)   — primary actions, active states, brand moments
Accent hover: amber-700 (#b45309)   — hover state on accent elements
Danger:       red-600   (#dc2626)   — destructive actions, error states
Success:      green-600 (#16a34a)   — success states, confirmed actions
Warning:      amber-500 (#f59e0b)   — warnings (distinct from accent)
```

Amber-600 is the single brand color. It appears on primary buttons, active nav states, and key status indicators. It does not appear on every interactive element — used sparingly, it reads as intentional rather than decorative.

### Typography

```
Font:         System font stack — Inter if available, then system-ui
              (No web font load = faster, especially on mobile)
Base size:    16px (1rem)
Scale:        text-sm (14px), text-base (16px), text-lg (18px),
              text-xl (20px), text-2xl (24px), text-3xl (30px)
Weight:       normal (400) for body, medium (500) for labels,
              semibold (600) for headings and CTAs
Line height:  leading-normal (1.5) for body, leading-tight (1.25) for headings
```

No decorative fonts. No custom font loading on the critical path. The Inter system font stack is clean, legible at small sizes, and renders well on both mobile and desktop.

### Spacing

Consistent 4px base unit via Tailwind spacing scale. All component internal padding uses multiples of this unit. Don't use arbitrary spacing values.

### Elevation

Two levels only — no shadow tower:
```
Resting:  shadow-sm   — cards, panels
Floating: shadow-lg   — modals, dropdowns, toasts
```

No shadow-md, no shadow-xl in the UI. Elevation is used to communicate "this is above the page" — not for decoration.

---

## Feedback patterns

These four patterns apply consistently across all surfaces. Never mix patterns arbitrarily — each has a defined purpose.

### 1. Inline validation

**Purpose:** field-level errors. Appears on blur (not on keystroke — don't punish the user mid-typing).

**Rules:**
- Error text appears directly below the offending field
- The field border changes to `border-red-500`
- The error message explains what is wrong AND what to do: "Email is required" is wrong. "Enter your email address to continue" is right.
- On correction, the error clears immediately (on keystroke, not on blur) — give positive feedback fast
- Never show more than one error per field at a time — show the most actionable error first

```html
<!-- templ component: components/field_error.templ -->
<div class="mt-1 flex items-center gap-1.5 text-sm text-red-600">
    <svg class="h-4 w-4 flex-shrink-0"><!-- exclamation icon --></svg>
    <span>{ message }</span>
</div>
```

Alpine drives the show/hide:
```html
<input
    x-model="email"
    @blur="validateEmail()"
    :class="emailError ? 'border-red-500' : 'border-zinc-200'"
    class="w-full rounded-md border px-3 py-2 text-sm"
/>
<template x-if="emailError">
    @components.FieldError(emailError)
</template>
```

### 2. Toast notifications

**Purpose:** confirmation that an action succeeded or a non-blocking background error occurred. Not for form validation errors.

**Rules:**
- Position: fixed bottom-right on desktop, bottom-center on mobile
- Auto-dismiss after 4 seconds for success, 6 seconds for errors
- Maximum 3 toasts visible at once — older ones push up and fade
- Always include a manual dismiss button — never trap a user waiting for auto-dismiss
- Success toast: green-600 left border, checkmark icon
- Error toast: red-600 left border, exclamation icon
- Error toasts for unresolvable errors include a brief explanation: "Subscription could not be paused. Our team has been notified." — not "An error occurred."

```html
<!-- components/toast.templ -->
<!-- Triggered via HX-Trigger response header or Alpine event -->
<div
    x-data="toast()"
    x-show="visible"
    x-transition:enter="transition ease-out duration-200"
    x-transition:enter-start="opacity-0 translate-y-2"
    x-transition:enter-end="opacity-100 translate-y-0"
    x-transition:leave="transition ease-in duration-150"
    x-transition:leave-end="opacity-0"
    class="fixed bottom-4 right-4 z-50 flex items-start gap-3 rounded-lg border-l-4 bg-white p-4 shadow-lg"
    :class="type === 'success' ? 'border-green-600' : 'border-red-600'"
>
    <!-- icon, message, dismiss button -->
</div>
```

Server-side actions trigger toasts via the `HX-Trigger` response header — no JavaScript required for the happy path:

```go
// web/respond.go
func TriggerToast(w http.ResponseWriter, toastType, message string) {
    payload := fmt.Sprintf(`{"showToast":{"type":%q,"message":%q}}`, toastType, message)
    w.Header().Set("HX-Trigger", payload)
}
```

### 3. Inline confirmation dialogs

**Purpose:** gate destructive or irreversible actions. Not a browser `confirm()` — a styled inline dialog.

**Rules:**
- Appears inline near the triggering element, not as a centered modal overlay
- Two buttons: a clearly-labelled confirm action (red background for destructive) and a cancel
- Confirm button text describes the action, not "OK": "Cancel subscription", not "Confirm"
- Disappears on cancel with no state change
- Used for: cancel subscription, delete address, remove cart item (if it would be hard to re-add)
- NOT used for: adding to cart, placing an order, changing address — these are not destructive

```html
<!-- Alpine inline confirm pattern -->
<div x-data="{ confirming: false }">
    <button
        x-show="!confirming"
        @click="confirming = true"
        class="text-sm text-red-600 hover:text-red-700"
    >
        Cancel subscription
    </button>
    <div x-show="confirming" class="flex items-center gap-2">
        <span class="text-sm text-zinc-600">Are you sure?</span>
        <button
            hx-post="/subscriptions/{{ .ID }}/cancel"
            hx-target="#subscription-{{ .ID }}"
            hx-swap="outerHTML"
            class="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700"
        >
            Cancel subscription
        </button>
        <button
            @click="confirming = false"
            class="text-sm text-zinc-500 hover:text-zinc-700"
        >
            Keep it
        </button>
    </div>
</div>
```

### 4. Progress indicators

**Purpose:** multi-step flows where the user needs orientation — checkout and subscription setup.

**Rules:**
- Show the step name, not just a number: "Shipping" not "Step 2 of 4"
- Completed steps are checkmarked and clickable (user can go back)
- Current step is highlighted with accent color
- Future steps are muted — visible but clearly inactive
- On mobile, show only the current step name and "Step N of N" — the full stepper collapses
- Progress is never lost on back navigation

```
Desktop:  [✓ Cart] → [✓ Shipping] → [● Payment] → [  Confirm]
Mobile:   Payment  ·  Step 3 of 4
```

---

## Error message philosophy

This deserves its own section because it is the most commonly done wrong.

### The three categories of errors

**Recoverable — user can fix it:**
Tell them exactly what is wrong and exactly what to do.
- ❌ "Invalid email address"
- ✅ "Enter a valid email address (e.g. name@example.com)"

**Recoverable — system can fix it automatically:**
Tell them what happened and what you're doing about it.
- ❌ "Request failed"
- ✅ "Your payment didn't go through. We're retrying — this usually resolves in a moment."

**Unrecoverable — neither user nor system can fix it right now:**
Tell them what happened, that it's not their fault, and what their options are.
- ❌ "An error occurred. Please try again."
- ✅ "We couldn't process your order right now. Your card has not been charged. Please try again in a few minutes or contact us at support@roaster.com."

### Error message rules

1. **Always name the problem.** "Something went wrong" is not a problem name. "Payment declined" is.
2. **Never blame the user** for system errors. "Your session expired" (system's fault) vs "You entered an incorrect password" (user's action, neutral tone).
3. **Always offer a next step.** Even if the next step is "contact support" — give them the path forward.
4. **Don't hide errors in the console.** If something failed, the user sees it. Silent failures erode trust faster than visible errors.
5. **Match severity to presentation.** A failed inline validation does not deserve a toast. A failed payment does not deserve only an inline field error.

### Error severity matrix

| Error type | Presentation | Auto-dismiss |
|---|---|---|
| Field validation failure | Inline field error | No |
| Form submission failure (user fixable) | Inline form-level error banner above submit button | No |
| Action success | Success toast | Yes — 4s |
| Action failure (retryable) | Error toast with retry suggestion | No |
| Action failure (unrecoverable) | Error toast with contact info | No |
| Page-level failure (404, 403, 500) | Full error page with explanation and navigation options | No |

---

## Component architecture

### Three tiers of components

**Tier 1 — Primitives** (`ui/components/`)
Atoms with no business knowledge. Reused everywhere across all four surfaces.

```
Button          — variant (primary, secondary, danger, ghost), size, loading state
Input           — text, email, number, with label, error state, help text
Select          — styled native select with consistent appearance
Textarea
Checkbox
Badge           — status indicator, color variants
Avatar          — customer/staff initials or image
Spinner         — loading state
Icon            — wrapper around SVG icons, consistent sizing
FieldError      — error message below a field
EmptyState      — zero-data states with illustration + message + optional CTA
```

**Tier 2 — Compositions** (`ui/components/`)
Built from primitives, still reusable across surfaces but carry more structure.

```
FormField       — Label + Input + FieldError composed together
Card            — surface container with consistent padding and border
DataTable       — header, rows, empty state, optional pagination
Pagination      — prev/next + page numbers
Modal           — overlay dialog with header, body, footer slots
Toast           — notification component (one instance, managed by Alpine)
PageHeader      — title + optional subtitle + optional action button
SectionHeader   — within-page section heading
StepIndicator   — multi-step progress (checkout, subscription setup)
OrderSummary    — line items + totals, reused in checkout and order history
AddressCard     — formatted address display + optional edit/delete actions
SubscriptionCard — subscription summary + status + action buttons
FlashBanner     — dismissible page-level message (below nav, above content)
```

**Tier 3 — Page sections** (inline in page templates, not shared)
Large compositions that are specific to one page. Not extracted to `components/` because they're only used once. If a page section starts appearing on a second page, it gets promoted to Tier 2.

### Component naming conventions

```
Exported (PascalCase)  → used across multiple pages or surfaces
Private (camelCase)    → defined in the same file as the page that uses it
```

A `productCard` component that only appears on the catalog page is lowercase and defined in `ui/storefront/catalog.templ`. The moment it's needed on the homepage too, it becomes `ProductCard` in `ui/components/`.

### The Button component

The most-used component. Gets full treatment upfront:

```
Variants:
  primary   — amber-600 bg, white text (one per page section)
  secondary — white bg, zinc-200 border, zinc-700 text
  danger    — red-600 bg, white text (destructive actions)
  ghost     — no bg, no border, zinc-600 text (tertiary actions)

Sizes:
  sm  — px-3 py-1.5 text-sm (inline actions, table rows)
  md  — px-4 py-2 text-sm (default, form submissions)
  lg  — px-6 py-3 text-base (primary CTAs)

States:
  default, hover, focus (ring), disabled (opacity-50, cursor-not-allowed), loading (spinner replaces text)
```

Loading state is important — any button that triggers an HTTP request should show a loading spinner between click and response. htmx makes this straightforward with `hx-indicator`.

---

## Surface-by-surface design

### Surface 1 — Storefront + Account (mobile-first)

**Layout:**
- Fixed top nav: logo left, cart right, hamburger menu on mobile
- On login: nav gains Account link with dropdown (orders, subscriptions, addresses, logout)
- Full-width on mobile, max-w-6xl centered on desktop
- Bottom padding accounts for mobile browser chrome

**Storefront nav:**
```
[Logo]                    [Account ▾] [Cart (2)]
                      ↓ mobile
[≡]    [Logo]                          [Cart]
```

**Key pages:**

*Catalog* — product grid. Mobile: 1 column. Desktop: 3 columns. Each card: product image, name, short description, price, "Subscribe" primary CTA, "One-time purchase" secondary. Subscriptions are the preferred action — "Subscribe" is larger and amber, one-time is ghost/secondary.

*Product detail* — image hero, name, origin story, roast profile. Subscription options are displayed first with pricing. One-time option is below. Quantity selector. "Add to cart" / "Subscribe". Never hide the price.

*Cart* — line items with quantity controls. Order summary (subtotal, shipping, tax, discount). Coupon code field. Prominent "Checkout" CTA. Guest users see a "Continue as guest" option alongside login — don't gate checkout behind account creation.

*Checkout* — Svelte component. Progress indicator: Cart → Shipping → Payment → Confirmation. Each step is completable before proceeding — no hidden validation that only fires at the end.

*Account — Order history* — reverse chronological list. Each order: date, order number, total, status badge, "View details" link. Order detail: full line items, address, invoice-style totals including tax/discount.

*Account — Subscriptions* — active subscriptions first. Each card shows: product, frequency, next order date, status. Actions: pause (with inline confirmation), skip next order, change frequency, cancel (with inline confirmation + reason select).

*Account — Addresses* — saved addresses. Default address marked. Edit/delete per address. Add new address.

**Mobile-specific considerations:**
- Tap targets minimum 44px height
- Form fields full-width, no side-by-side fields on mobile
- Cart accessible without leaving the current page (slide-in drawer)
- Sticky checkout button on cart page — always visible without scrolling

### Surface 2 — Wholesale portal (desktop-first)

Wholesale buyers are placing bulk orders, often repeat orders of known products. The UX priority is **speed and efficiency** — not discovery.

**Layout:**
- Sidebar nav (fixed left, 240px) — product catalog, orders, account, contact
- Main content area (remaining width)
- Dense table layouts acceptable — these users are at desks

**Key pages:**

*Quick order* — the most important page. A table of all available products with quantity inputs inline. Customer types quantities directly into the table, hits "Add to cart". No browsing, no product detail pages required. One click from login to order placed.

```
Product          | SKU      | Unit Price | Min Qty | Qty | Subtotal
─────────────────────────────────────────────────────────────────
Ethiopia Yirgacheffe | ETH-001 | $18.00/lb | 5 lbs | [10] | $180.00
Colombia Huila      | COL-002 | $16.50/lb | 5 lbs | [ 5] | $82.50
                                                         ─────────
                                                   Total: $262.50
                                           [Add selected to cart →]
```

*Order history* — full order history with search, date filter, status filter. Repeat order button on each past order — one click to add all items from a previous order to the current cart.

*Account* — wholesale pricing tier display, credit terms if applicable, contact information.

**Wholesale-specific feedback:**
- Minimum quantity violations shown inline in the table (red border on the qty input, helper text showing minimum)
- Pricing tier thresholds shown: "Spend $500 more to reach Tier 2 pricing"
- Out of stock items shown in the table but inputs disabled with "Out of stock" badge

### Surface 3 — Customer account panel

Already partially covered in Surface 1 (order history, subscriptions, addresses). This surface is the authenticated layer of the storefront.

The key distinction from the storefront: the customer account panel is **task-oriented**, not discovery-oriented. Navigation should put the most common tasks first:

```
Account nav (sidebar on desktop, bottom tabs on mobile):
  Orders        ← most visited
  Subscriptions ← second most visited
  Addresses
  Payment methods (future)
  Profile
```

**Subscription management is the crown jewel of this surface.** It must be clear, low-anxiety, and low-friction. Customers who can easily pause or skip their subscription without contacting support are more likely to stay subscribed long-term than customers who feel trapped.

Rules for subscription actions:
- **Pause**: ask how long (2 weeks, 1 month, custom date). Confirm with "Resume on [date]" shown.
- **Skip next**: single click, confirm inline. "Your next order on [date] will be skipped. The following order will ship on [date]."
- **Cancel**: require a reason (dropdown — not a trick, genuine signal for the merchant). Then a one-click "pause instead?" offer before confirming cancel. If they still cancel, confirm clearly: "Your subscription has been cancelled. You can reactivate at any time."

### Surface 4 — Staff admin (desktop-first, high density)

The admin is a tool, not a storefront. Aesthetic is Clean Utility at its most utilitarian. Information density is high.

**Layout:**
- Fixed sidebar nav (240px): Dashboard, Orders, Customers, Catalog, Subscriptions, Fulfillment, Discounts, Settings
- Top bar: search, notifications, staff avatar/logout
- Content area: full remaining width

**Dashboard** — the first thing staff sees on login. Key metrics at a glance (not a data dump):

```
[Orders today: 12]  [Revenue today: $1,840]  [Active subscriptions: 247]  [Renewals due: 8]

Recent orders (last 10) — quick status overview
Pending fulfillment queue — orders ready to ship
```

**Orders** — filterable table. Status, customer, date, total, fulfillment status. Click through to order detail. Order detail: full line items, customer info, address, payment status, fulfillment status, audit log sidebar.

**Customers** — searchable table. Click through to customer detail: order history, subscriptions, addresses, tax exemption status. Tax exemption grant/revoke is a clearly-labelled admin action with a reason field — not buried in settings.

**Fulfillment** — orders ready to ship, grouped by status. Label generation inline on each order.

**Data tables throughout:**
- Sortable column headers
- Persistent filter state (stored in URL params so links are shareable)
- Bulk select + bulk action for common operations (mark shipped, export)
- Empty states explain why the table is empty and what action to take

---

## htmx usage patterns across all surfaces

**What htmx handles:**
- Form submissions that update a section of the page (subscription pause, address save, coupon code application)
- List filtering and pagination without full page reload
- Order status updates in the admin
- Quantity changes in the wholesale quick order table
- Cart item removal and quantity adjustment

**What htmx does NOT handle:**
- Multi-step checkout flow (Svelte)
- Local open/close state (Alpine)
- Real-time anything — htmx polling is a last resort

**Standard htmx response conventions:**

```go
// Successful action → return the updated component fragment + trigger toast
w.Header().Set("HX-Trigger", `{"showToast":{"type":"success","message":"Subscription paused"}}`)
web.Render(w, r, ui.SubscriptionCard(updated))

// Validation failure → return the form with inline errors, 422 status
w.WriteHeader(http.StatusUnprocessableEntity)
web.Render(w, r, ui.SubscriptionPauseForm(form, errors))

// Redirect after action → HX-Redirect header
w.Header().Set("HX-Redirect", "/account/subscriptions")
w.WriteHeader(http.StatusNoContent)
```

The 422 status on validation failure is important — htmx will swap the response into the target on 422, showing the form with errors. Without it (returning 200 with errors), some htmx configurations won't swap on error responses.

---

## Alpine.js usage patterns

**Alpine owns:**
- Open/close (dropdowns, mobile menu, cart drawer, modal visibility)
- Inline confirmation state (`confirming: false` → `true`)
- Form field visibility (show/hide based on another field's value)
- Toast queue management (add, auto-dismiss, manual dismiss)
- Step indicator state in checkout (mirrored from Svelte's state via a custom event)

**Alpine does NOT own:**
- Application data (orders, products, customer info) — that comes from the server
- Form submission — htmx handles that
- Business logic — no price calculations, no eligibility checks

**Global Alpine components** (defined in the storefront layout's `<script>` block):

```javascript
Alpine.data('toast', () => ({
    toasts: [],
    add(type, message) {
        const id = Date.now()
        this.toasts.push({ id, type, message, visible: true })
        if (type === 'success') {
            setTimeout(() => this.remove(id), 4000)
        } else {
            setTimeout(() => this.remove(id), 6000)
        }
    },
    remove(id) {
        this.toasts = this.toasts.filter(t => t.id !== id)
    }
}))

// Listens for HX-Trigger showToast events
document.addEventListener('showToast', (e) => {
    Alpine.store('toast').add(e.detail.type, e.detail.message)
})
```

---

## templ file organization

```
internal/ui/
  layouts/
    storefront.templ      ← storefront + account layout (shared nav, footer)
    wholesale.templ       ← wholesale portal layout (sidebar nav)
    admin.templ           ← staff admin layout (sidebar nav, top bar)
  components/
    button.templ          ← Tier 1 primitives
    input.templ
    badge.templ
    spinner.templ
    empty_state.templ
    field_error.templ
    form_field.templ      ← Tier 2 compositions
    card.templ
    data_table.templ
    pagination.templ
    modal.templ
    toast.templ
    page_header.templ
    step_indicator.templ
    order_summary.templ
    address_card.templ
    subscription_card.templ
    flash_banner.templ
  storefront/
    catalog.templ
    product.templ
    cart.templ
    checkout.templ        ← Svelte mount point
    auth.templ
  account/
    orders.templ
    order_detail.templ
    subscriptions.templ
    addresses.templ
  wholesale/
    quick_order.templ
    orders.templ
    account.templ
  admin/
    dashboard.templ
    orders.templ
    order_detail.templ
    customers.templ
    customer_detail.templ
    catalog.templ
    subscriptions.templ
    fulfillment.templ
    discounts.templ
    settings.templ
```

---

## Build priorities

Build in this order. Each phase delivers a usable surface.

**Phase 1 — Design system foundations**
Primitives and Tier 2 compositions. Button, Input, FormField, Badge, Card, DataTable, Pagination, Toast, EmptyState. Build these with placeholder data before building any real page. Every subsequent page is assembled from these — investing here pays off on every page that follows.

**Phase 2 — Staff admin**
Build the admin before the storefront. Reason: the admin is how the merchant populates data (products, pricing, customers). You need it working to have realistic data to test the storefront against. Admin pages are also simpler to build — no mobile concerns, no SEO, no performance anxiety.

Order within admin: Dashboard → Orders → Customers → Catalog → Fulfillment → Discounts → Settings.

**Phase 3 — Storefront (B2C)**
Catalog → Product detail → Cart → Checkout (Svelte) → Auth (login/register). This is the revenue path — get it right before the account panel.

**Phase 4 — Customer account panel**
Order history → Subscriptions → Addresses. Subscription management is the most complex — build order history first for simpler wins.

**Phase 5 — Wholesale portal**
Quick order → Order history → Account. B2B portal is last because it serves a smaller audience and the foundation (products, orders, customers) must already exist.

---

## What this document does not cover

- **Specific Tailwind class lists** — those live in the templ components themselves
- **Svelte checkout component internals** — covered in the package structure document
- **SEO and meta tags** — handled in the storefront layout, not a component concern
- **Email templates** — separate concern, not part of the templ component system
- **Dark mode** — not in scope; Clean Utility is a light-mode-first system