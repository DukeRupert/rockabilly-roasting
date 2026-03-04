# Hiri UI Direction

## Design Philosophy

Hiri's interface is **pragmatic craft**—a reliable tool that respects both the user's time and their trade. The UI stays out of the way, communicates clearly, and carries subtle warmth that acknowledges the human scale of small-batch roasting.

This is software for busy people who care about quality. It should feel capable without being corporate, warm without being cute.

---

## Principles

### 1. Respect the operator

Users are often mid-task—between roasts, packing orders, answering wholesale inquiries. The interface should support quick, confident action. Don't make them hunt for things or confirm the obvious.

### 2. Clarity over cleverness

Every label, message, and interaction should be immediately understood. Avoid jargon, abbreviations, and ambiguous icons. When in doubt, use words.

### 3. Calm confidence

The UI should feel steady and trustworthy. No unnecessary animations, no excitement where none is warranted. When something succeeds, confirm it simply. When something fails, explain it plainly.

### 4. Warmth in the details

Small touches—a helpful empty state, a well-written confirmation, comfortable spacing—add up to an interface that feels human. This warmth is subtle, never performative.

### 5. Density with breathing room

Show enough information to be useful without overwhelming. Tables and lists should be scannable. Forms should not feel cramped. Whitespace is a feature.

---

## Visual Language

### Color Palette

The palette draws subtle inspiration from Hiri Moale festival colors—the red ochre and white of ceremonial body paint, the golden tan of grass skirts and sago palm sails, the deep browns of clay pots and lakatoi hulls, and the coastal waters of the Gulf of Papua. These influences appear in muted, professional form.

**Base colors (used most frequently):**

| Role | Color | Usage |
|------|-------|-------|
| Background | Warm off-white (#FAF9F7) | Page backgrounds—a hint of the cream found in kina shells |
| Surface | White (#FFFFFF) | Cards, panels, inputs |
| Border | Warm gray (#E5E2DE) | Dividers, input borders |
| Text primary | Deep brown-black (#1C1917) | Headings, body text—echoes lakatoi wood |
| Text secondary | Warm gray (#78716C) | Labels, helper text, timestamps |

**Accent colors (used sparingly):**

| Role | Color | Usage |
|------|-------|-------|
| Primary action | Ocean teal (#2D7A7A) | Primary buttons, links, focus states—the Gulf waters |
| Primary hover | Deep teal (#1E5E5E) | Button hover states |
| Secondary accent | Sunset amber (#C4873A) | Highlights, badges—the lakatoi returning at dusk |

**Semantic colors:**

| Role | Color | Usage |
|------|-------|-------|
| Success | Coastal green (#3D8B6E) | Success messages, positive status |
| Warning | Trade amber (#C4873A) | Warnings, pending states |
| Error | Ochre red (#B85C4A) | Errors, destructive actions—muted red ochre |
| Info | Water blue (#4A7FA8) | Informational messages |

**Heritage tones (optional accents):**

| Name | Color | Inspiration | Usage |
|------|-------|-------------|-------|
| Uro clay | Terracotta (#A66B4B) | Traditional clay pots | Subtle accents, illustrations |
| Kina | Ivory cream (#F5F0E6) | Pearl shell currency | Alternative backgrounds, highlights |
| Lakatoi | Deep wood brown (#3D2E27) | Trading canoe hulls | Footer, dark accents |
| Sail tan | Golden sand (#D4C4A8) | Sago palm sails | Warm highlights, empty states |

**Usage guidelines:**
- The interface should feel predominantly neutral with color used purposefully
- Primary accent (teal) is reserved for interactive elements and focus
- Heritage tones are for subtle warmth, not theming—use very sparingly
- Avoid using multiple accent colors in close proximity
- Semantic colors appear only when conveying status or feedback
- The palette should feel warm and grounded, never cold or stark

### Logo

The logo combines a stylized double crab-claw sail mark with the Hiri wordmark.

**Mark:** Two overlapping sail shapes inspired by lakatoi crab-claw sails. The back sail renders at 35% opacity to create depth.

**Wordmark:** "hiri" in lowercase Outfit Semibold.

| Element | Specification |
|---------|---------------|
| Typeface | Outfit (Google Fonts) |
| Weight | Semibold (600) |
| Case | lowercase |
| Letter spacing | -0.01em (standard), 0.25em (app icon) |
| Primary color | #2D7A7A (Gulf teal) |
| Back sail opacity | 35% (logo), 40% (app icon) |

**Variants:**
- **Primary**: Teal mark + dark wordmark on light background
- **Dark mode**: Off-white mark + wordmark on dark background
- **Brand background**: Off-white mark + wordmark on teal background
- **Monochrome**: Single color (dark or light) for both elements

**App icon:** Sails + wordmark on teal rounded square (rx="14" on 64px = ~22% radius). At sizes below 32px, use sails-only version for legibility.

**Clear space:** Maintain padding equal to the height of the "h" in the wordmark on all sides.

**Usage:**
- Don't stretch, rotate, or alter proportions
- Don't change the sail opacity relationship
- Don't use colors outside the defined palette
- Don't add effects (shadows, gradients, outlines)

See `planning/LOGO.html` for visual reference and SVG source.

### Typography

**Font stack:** System sans-serif

```css
font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
```

Using system fonts ensures fast loading, familiar rendering, and no external dependencies.

**Scale:**

| Role | Size | Weight | Usage |
|------|------|--------|-------|
| Page title | 24px / 1.75rem | 600 | Main page headings |
| Section title | 18px / 1.25rem | 600 | Card headings, section breaks |
| Body | 15px / 1rem | 400 | Default text, table cells |
| Label | 14px / 0.875rem | 500 | Form labels, table headers |
| Small | 13px / 0.8125rem | 400 | Helper text, timestamps, metadata |
| Tiny | 12px / 0.75rem | 500 | Badges, status indicators |

**Guidelines:**
- Line height: 1.5 for body text, 1.3 for headings
- Avoid bold for emphasis in running text; use it for labels and headings
- Don't use all-caps except for small badges or status indicators

### Spacing

Use a consistent spacing scale based on 4px increments:

| Token | Value | Usage |
|-------|-------|-------|
| xs | 4px | Tight spacing, icon padding |
| sm | 8px | Related elements, input padding |
| md | 16px | Standard gaps, section padding |
| lg | 24px | Card padding, major sections |
| xl | 32px | Page margins, section breaks |
| 2xl | 48px | Major page sections |

**Guidelines:**
- Be generous with padding inside cards and panels
- Group related items closely; separate unrelated items clearly
- Maintain consistent margins throughout a page

### Border Radius

| Element | Radius |
|---------|--------|
| Buttons | 6px |
| Inputs | 6px |
| Cards | 8px |
| Badges | 4px |
| Modals | 12px |

Avoid pill shapes (fully rounded) except for small status dots. The interface should feel grounded, not bubbly.

### Shadows

Use shadows sparingly. Most elements should rely on borders and background contrast.

| Usage | Shadow |
|-------|--------|
| Cards (subtle lift) | 0 1px 3px rgba(0,0,0,0.06) |
| Dropdowns, modals | 0 4px 12px rgba(0,0,0,0.10) |
| Hover states | 0 2px 6px rgba(0,0,0,0.08) |

---

## Components

### Buttons

**Primary button:**
- Background: Primary accent (teal)
- Text: White
- Used for: Main action on a page (Save, Create, Submit)
- One primary button per view

**Secondary button:**
- Background: White
- Border: Light gray
- Text: Text primary
- Used for: Secondary actions (Cancel, Back, alternative paths)

**Destructive button:**
- Background: White (default) or Error red (confirmation)
- Border: Error red
- Text: Error red
- Used for: Delete, Remove, irreversible actions
- Require confirmation for destructive actions

**Ghost button:**
- Background: Transparent
- Text: Primary accent or text secondary
- Used for: Tertiary actions, inline actions in tables

**Button sizing:**

| Size | Padding | Font size |
|------|---------|-----------|
| Small | 6px 12px | 13px |
| Default | 10px 16px | 15px |
| Large | 12px 20px | 16px |

### Forms

**Text inputs:**
- Border: Light gray, 1px
- Border radius: 6px
- Padding: 10px 12px
- Focus: Primary accent border, subtle shadow
- Error: Error red border, error message below

**Labels:**
- Position: Above input
- Weight: 500
- Size: 14px
- Include helper text below label when needed, not as placeholder

**Layout:**
- Stack labels and inputs vertically
- Group related fields visually
- Use consistent field widths within a form
- Place primary action at the end, left-aligned

### Tables

**Structure:**
- Header row: Light background (#F8F7F6), label typography
- Body rows: White background, subtle border between rows
- Row hover: Very subtle highlight (#FAFAF8)
- Padding: 12px horizontal, 10px vertical per cell

**Guidelines:**
- Right-align numeric columns (prices, quantities)
- Left-align text columns
- Include clear column headers
- Provide empty state when no data (not just blank space)
- Pagination below table when needed

### Cards

**Default card:**
- Background: White
- Border: Light gray, 1px (or subtle shadow, not both)
- Border radius: 8px
- Padding: 24px

**Guidelines:**
- Use cards to group related content
- Don't nest cards within cards
- Card headers should use section title typography

### Status Indicators

**Badges:**
- Small (12px font), medium weight, 4px radius
- Subtle background tint with matching text color
- Examples: "Active" (green tint), "Pending" (amber tint), "Overdue" (red tint)

**Status dots:**
- 8px circle, semantic color
- Pair with text label for accessibility

---

## Patterns

### Page Layout

**Standard page structure:**

```
┌─────────────────────────────────────────────┐
│ Page Title                        [Action]  │
│ Optional description text                   │
├─────────────────────────────────────────────┤
│                                             │
│  Main content area                          │
│                                             │
│  - Cards, tables, forms                     │
│                                             │
└─────────────────────────────────────────────┘
```

- Page title top-left, primary action top-right
- Description text (when needed) below title in secondary color
- Generous padding around content area

### Empty States

Empty states should be helpful, not sad or jokey.

**Structure:**
- Brief statement of what's empty
- Guidance on what to do next
- Action button when applicable

**Example:**

```
No products yet

Products you create will appear here. Start by adding your first coffee.

[Add Product]
```

### Confirmation and Feedback

**Success messages:**
- Appear inline or as toast notification
- Disappear automatically after 4-5 seconds
- Keep copy brief: "Product created" not "Your product has been successfully created!"

**Error messages:**
- Appear inline near the problem when possible
- Persist until resolved
- Explain what went wrong and how to fix it

**Destructive confirmations:**
- Use a modal for irreversible actions
- Clearly state what will happen
- Make the destructive action visually distinct (red text or button)
- Example: "Delete Mountain Blend? This will remove the product and all its variants. Orders containing this product will not be affected."

### Loading States

- Use skeleton screens for initial page loads when possible
- Use spinner for actions (button loading state)
- Disable buttons while action is processing
- Avoid full-page spinners

---

## Voice and Tone

### General Guidelines

- Be direct and concise
- Use plain language
- Address the user as "you" when needed
- Avoid exclamation points except in genuinely celebratory moments
- Don't anthropomorphize the software ("We're working on it!")

### Specific Contexts

**Page titles and navigation:**
- Use nouns: "Products", "Orders", "Customers"
- Not verbs: "Manage Products", "View Orders"

**Button labels:**
- Use clear verbs: "Save", "Create", "Delete", "Send Invoice"
- Not vague: "Submit", "OK", "Done" (unless truly generic)

**Form labels:**
- Be specific: "Business name", "Roast level", "Price per pound"
- Include units or format hints in helper text

**Confirmations:**
- State what happened: "Order marked as shipped"
- Include next step when relevant: "Product created. Add pricing to make it available."

**Errors:**
- State the problem simply: "Couldn't save changes"
- Provide guidance: "Check your connection and try again"
- Never blame the user

### Examples

| Context | ❌ Avoid | ✅ Use |
|---------|---------|--------|
| Product created | "Awesome! Your product is ready!" | "Product created" |
| Empty orders | "No orders yet 😢" | "No orders yet" |
| Form error | "Oops! Something went wrong" | "Couldn't save. Price is required." |
| Delete confirm | "Are you sure?" | "Delete Mountain Blend? This can't be undone." |
| Loading | "Hang tight..." | (spinner, no text) |
| Success toast | "Changes saved successfully!" | "Changes saved" |

---

## Accessibility

### Requirements

- Color contrast: Minimum 4.5:1 for body text, 3:1 for large text
- Focus indicators: Visible focus ring on all interactive elements
- Labels: All form inputs have associated labels (not just placeholders)
- Alt text: All meaningful images have descriptive alt text
- Keyboard navigation: All actions accessible via keyboard

### Guidelines

- Don't rely on color alone to convey meaning (pair with icons or text)
- Ensure touch targets are at least 44x44px on mobile
- Test with keyboard navigation regularly
- Use semantic HTML (buttons for actions, links for navigation)

---

## Responsive Behavior

### Breakpoints

| Name | Width | Target |
|------|-------|--------|
| Mobile | < 640px | Phones |
| Tablet | 640px - 1024px | Tablets, small laptops |
| Desktop | > 1024px | Laptops, desktops |

### Adaptation

**Navigation:**
- Desktop: Persistent sidebar
- Mobile: Collapsible menu (hamburger)

**Tables:**
- Desktop: Full table
- Mobile: Card-based list or horizontal scroll

**Forms:**
- Desktop: Multi-column when logical
- Mobile: Single column, full width inputs

**Page actions:**
- Desktop: Top-right of page header
- Mobile: Bottom fixed bar or within page flow

---

## Brand Heritage

The Hiri name comes from the traditional trade voyages of the Motu people of Papua New Guinea. The UI doesn't need to be overtly themed around this heritage, but the visual language carries subtle echoes of the festival and its traditions.

**What the name brings:**
- A sense of purposeful exchange—commerce as connection
- Warmth and humanity (trade between communities, not corporations)
- Groundedness in craft and tradition

**Color inspiration:**
The palette draws from Hiri Moale festival imagery—the red ochre and white of ceremonial body paint, the golden grass skirts swaying in the Hiri Hanenamo dance, the terracotta of traded clay pots (uro), the deep wood of lakatoi hulls, and the coastal teal of Gulf waters. These appear in muted, professional form throughout the interface.

**Subtle nods:**
- Logo: The double crab-claw sail mark references the distinctive sails of lakatoi trading canoes
- Accent palette: Warm, earthy tones complement the coffee context and echo the coastal/trade heritage
- Language: The word "hiri" itself is the primary brand expression

**Avoid:**
- Literal Pacific imagery (canoes, waves, tribal patterns) in the UI
- Tiki or "island" aesthetic
- Anything that feels like cultural costume or tourism
- Overexplaining the name's origin in the interface

The brand is named Hiri; the UI is a professional tool for coffee businesses. The heritage gives the name meaning and warmth without dictating the visual design.

---

## Reference Interfaces

These products demonstrate elements of the desired direction:

| Product | What to reference |
|---------|-------------------|
| Linear | Information density, keyboard-first feel, calm palette |
| Stripe Dashboard | Table design, clear hierarchy, professional warmth |
| Notion | Clean typography, comfortable spacing, understated |
| Basecamp | Friendly but professional tone, opinionated simplicity |

---

## Summary

Hiri's UI should feel like a well-made tool: reliable, clear, and quietly confident. It respects the user's expertise and time. Warmth comes through clarity and helpfulness, not decoration or personality performance.

When making UI decisions, ask:
- Does this help the user complete their task?
- Is this the simplest way to communicate this?
- Would a busy roaster between batches appreciate this, or be annoyed by it?

If the answer to any of these is uncertain, simplify.