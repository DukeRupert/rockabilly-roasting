---
name: firefly-stack-agent
description: "Coding agent for the Firefly Software standard web stack: Go + templ + htmx + Alpine.js + Tailwind CSS, with Svelte 5 for complex frontend islands. Use this skill when writing, reviewing, or architecting code for any Firefly product or client project. Covers routing, templating, database access, frontend interactivity patterns, project conventions, and commenting standards. Read this before generating any code."
---

# Firefly Stack Agent

Coding assistant for the Firefly Software standard stack. Read this skill before
writing any code. Follow every section — they build on each other.

---

## Core Principles

1. **Walk before running.** Propose a plan in numbered steps before writing code.
   Each step should be independently testable. Wait for confirmation before
   implementing unless the task is trivially small (single function, no side effects).

2. **Minimal dependencies.** Reach for the standard library first. Add a dependency
   only when the standard library genuinely cannot do the job. Flag every new
   dependency and explain why it is necessary before adding it.

3. **Incremental and reversible.** Each step should produce a clean git commit.
   If a step is large enough that rolling it back would be painful, split it smaller.

4. **Comment for the next agent.** Every exported function, every handler, every
   template component, and any non-obvious internal function must have a comment
   explaining its purpose. Other agents perform quality control passes using these
   comments. Treat them as contracts, not documentation afterthoughts.

5. **Discuss before escalating.** If a decision requires upgrading the database,
   adding a framework, or meaningfully increasing complexity, surface it as a
   recommendation and explain the tradeoff. Never silently escalate.

---

## The Stack

| Layer | Tool | Notes |
|---|---|---|
| Language | Go 1.22+ | Use modern routing features (`net/http` ServeMux patterns) |
| Templating | `templ` | `.templ` files, type-safe components |
| Frontend interactivity | htmx + Alpine.js | Boundary described below |
| CSS | Tailwind CSS | Utility-first, no custom CSS unless unavoidable |
| Database | SQLite via `sqlc` | Default. Discuss Postgres upgrade when warranted. |
| Email | Postmark | Transactional only |
| Reverse proxy | Caddy | Config lives on host, not in Docker labels |
| Containers | Docker Compose | Standard for all Firefly services |
| Error tracking | Bugsink | `bs.fireflysoftware.dev` |

---

## Go Conventions

### Routing

Use Go 1.22+ `net/http` ServeMux with method and path patterns. No third-party
router unless there is a specific capability gap that cannot be addressed with
the standard library.

```go
// RegisterRoutes wires all application routes to the provided ServeMux.
// Handlers are organized by feature area. Each route registers its HTTP method
// explicitly to prevent accidental cross-method access.
func RegisterRoutes(mux *http.ServeMux, h *Handlers) {
    mux.HandleFunc("GET /", h.Home)
    mux.HandleFunc("GET /invoices", h.InvoiceList)
    mux.HandleFunc("POST /invoices", h.InvoiceCreate)
    mux.HandleFunc("GET /invoices/{id}", h.InvoiceDetail)
    mux.HandleFunc("DELETE /invoices/{id}", h.InvoiceDelete)
}
```

Use path values via `r.PathValue("id")` — not URL parsing libraries.

### Handlers

Handlers are methods on a `Handlers` struct that carries shared dependencies
(db, config, mailer, etc.). Never use package-level variables for dependencies.

```go
// Handlers holds all HTTP handler dependencies. Construct once at startup
// and pass to RegisterRoutes. All handlers are methods on this type.
type Handlers struct {
    DB     *sql.DB
    Queries *db.Queries // sqlc-generated
    Config  Config
    Mailer  Mailer
}

// InvoiceList handles GET /invoices. Returns the full invoice list rendered
// via templ. Supports htmx partial requests — when the HX-Request header is
// present, renders only the list fragment rather than the full page shell.
func (h *Handlers) InvoiceList(w http.ResponseWriter, r *http.Request) {
    invoices, err := h.Queries.ListInvoices(r.Context())
    if err != nil {
        http.Error(w, "failed to load invoices", http.StatusInternalServerError)
        return
    }

    if r.Header.Get("HX-Request") == "true" {
        component := views.InvoiceListFragment(invoices)
        component.Render(r.Context(), w)
        return
    }

    component := views.InvoicePage(invoices)
    component.Render(r.Context(), w)
}
```

### Error handling

Return errors from internal functions. Handle them at the handler boundary.
Do not `log.Fatal` in library code. Do not swallow errors silently.

```go
// fetchInvoice retrieves a single invoice by ID from the database.
// Returns a not-found sentinel error if the invoice does not exist,
// so callers can distinguish between missing records and query failures.
func fetchInvoice(ctx context.Context, q *db.Queries, id int64) (db.Invoice, error) {
    inv, err := q.GetInvoice(ctx, id)
    if errors.Is(err, sql.ErrNoRows) {
        return db.Invoice{}, ErrNotFound
    }
    if err != nil {
        return db.Invoice{}, fmt.Errorf("fetchInvoice: %w", err)
    }
    return inv, nil
}
```

### Project structure

Follow this layout for all Firefly applications:

```
/
├── cmd/
│   └── server/
│       └── main.go          # Entry point. Wires dependencies, starts server.
├── internal/
│   ├── db/                  # sqlc-generated code. Do not edit by hand.
│   │   ├── db.go
│   │   ├── models.go
│   │   └── queries.sql.go
│   ├── handlers/
│   │   ├── handlers.go      # Handlers struct and RegisterRoutes
│   │   └── {feature}.go     # One file per feature area
│   └── views/               # templ components
│       ├── layout.templ     # Page shell, nav, head
│       └── {feature}.templ  # Feature-specific components
├── migrations/              # SQL migration files, numbered sequentially
├── queries/                 # .sql query files for sqlc
├── schema.sql               # Full schema definition
├── sqlc.yaml
├── Dockerfile
├── docker-compose.yml
└── Caddyfile
```

---

## Database — SQLite via sqlc

### Default to SQLite

SQLite is the default. It is sufficient for all single-server Firefly deployments
and eliminates infrastructure complexity. Use `mattn/go-sqlite3` or
`modernc.org/sqlite` (pure Go, no CGo dependency — prefer this).

Enable WAL mode at startup:

```go
// configureDB sets SQLite pragmas required for production use.
// WAL mode enables concurrent reads alongside writes.
// Foreign keys are disabled by default in SQLite and must be enabled explicitly.
func configureDB(db *sql.DB) error {
    pragmas := []string{
        "PRAGMA journal_mode=WAL;",
        "PRAGMA foreign_keys=ON;",
        "PRAGMA busy_timeout=5000;",
    }
    for _, p := range pragmas {
        if _, err := db.Exec(p); err != nil {
            return fmt.Errorf("configureDB pragma %q: %w", p, err)
        }
    }
    return nil
}
```

### When to discuss upgrading to Postgres

Raise the question (do not silently upgrade) when any of the following are true:

- Multiple application servers need to share the same database
- Write volume is high enough that WAL contention becomes measurable
- Full-text search requirements exceed what SQLite FTS5 can handle cleanly
- A feature genuinely requires a Postgres-specific capability (e.g., LISTEN/NOTIFY,
  JSONB operators, PostGIS)

When raising it: state the specific trigger, estimate the migration cost, and
let Logan decide.

### sqlc usage

Write SQL queries in `/queries/{feature}.sql`. Generate Go code with `sqlc generate`.
Never write raw SQL in handler or business logic code — always go through sqlc queries.

```sql
-- name: ListInvoices :many
-- Returns all invoices ordered by creation date descending.
-- Used by InvoiceList handler for the full invoice index view.
SELECT * FROM invoices
ORDER BY created_at DESC;

-- name: GetInvoice :one
-- Fetches a single invoice by primary key.
-- Returns sql.ErrNoRows if not found — callers must handle this case.
SELECT * FROM invoices WHERE id = ?;
```

---

## Templating — templ

All HTML is authored in `.templ` files. Never use `html/template` directly.
The type safety from `templ` is the primary defense against template hallucinations
and missing data bugs.

### Component conventions

```templ
// InvoicePage renders the full invoice list page including the layout shell.
// Takes a slice of invoices for the initial server-side render.
// The list fragment is also available separately for htmx partial swaps.
templ InvoicePage(invoices []db.Invoice) {
    @Layout("Invoices") {
        <div class="max-w-4xl mx-auto px-4 py-8">
            <div class="flex items-center justify-between mb-6">
                <h1 class="text-2xl font-semibold text-gray-900">Invoices</h1>
                <button
                    hx-get="/invoices/new"
                    hx-target="#modal"
                    hx-swap="innerHTML"
                    class="btn-primary"
                >
                    New Invoice
                </button>
            </div>
            @InvoiceListFragment(invoices)
        </div>
        <div id="modal"></div>
    }
}

// InvoiceListFragment renders only the invoice table rows.
// Used for htmx partial swaps after create, update, or delete operations.
// Must be renderable independently of the page shell.
templ InvoiceListFragment(invoices []db.Invoice) {
    <div id="invoice-list" class="divide-y divide-gray-200">
        for _, inv := range invoices {
            @InvoiceRow(inv)
        }
    </div>
}

// InvoiceRow renders a single invoice as a table row.
// Includes htmx delete trigger targeting the row itself for removal on success.
templ InvoiceRow(inv db.Invoice) {
    <div id={ fmt.Sprintf("invoice-%d", inv.ID) } class="py-4 flex items-center justify-between">
        <span class="font-mono text-sm text-gray-600">{ inv.Number }</span>
        <span class="text-gray-900">{ inv.ClientName }</span>
        <button
            hx-delete={ fmt.Sprintf("/invoices/%d", inv.ID) }
            hx-target={ fmt.Sprintf("#invoice-%d", inv.ID) }
            hx-swap="outerHTML"
            hx-confirm="Delete this invoice?"
            class="text-red-600 text-sm hover:underline"
        >
            Delete
        </button>
    </div>
}
```

### Layout convention

```templ
// Layout wraps all pages in the application shell: <html>, <head>, nav, footer.
// The title parameter sets the <title> tag and the page heading context.
// All pages should render through Layout rather than duplicating shell HTML.
templ Layout(title string) {
    <!DOCTYPE html>
    <html lang="en">
        <head>
            <meta charset="UTF-8"/>
            <meta name="viewport" content="width=device-width, initial-scale=1.0"/>
            <title>{ title } — Manifest</title>
            <link rel="stylesheet" href="/static/app.css"/>
            <script src="/static/htmx.min.js" defer></script>
            <script src="/static/alpine.min.js" defer></script>
        </head>
        <body class="bg-gray-50 text-gray-900 antialiased">
            @Nav()
            <main>
                { children... }
            </main>
        </body>
    </html>
}
```

---

## Frontend Interactivity — htmx + Alpine.js

### The boundary

**Use htmx** when the interaction involves the server: fetching data, submitting
a form, deleting a record, loading a partial, paginating results. htmx is the
default tool for anything that touches the database or application state.

**Use Alpine.js** when the interaction is purely client-side UI state: toggling
a dropdown, showing/hiding a panel, managing tab state, controlling a modal's
open/closed state, form validation feedback before submission.

**The test:** Does this interaction need to talk to the server?
- Yes → htmx
- No → Alpine.js
- Both (e.g., a form with live validation AND a server submit) → Alpine.js for
  the validation state, htmx for the submit

### htmx patterns

```html
<!-- Load a partial into a target on click -->
<button
    hx-get="/invoices/new"
    hx-target="#modal"
    hx-swap="innerHTML"
>
    New Invoice
</button>

<!-- Submit a form and replace a list -->
<form
    hx-post="/invoices"
    hx-target="#invoice-list"
    hx-swap="outerHTML"
>
    ...
</form>

<!-- Delete and remove the element itself -->
<button
    hx-delete="/invoices/42"
    hx-target="#invoice-42"
    hx-swap="outerHTML"
    hx-confirm="Delete this invoice?"
>
    Delete
</button>

<!-- Infinite scroll / load more -->
<div
    hx-get="/invoices?page=2"
    hx-trigger="revealed"
    hx-target="#invoice-list"
    hx-swap="beforeend"
>
</div>
```

Always set `hx-target` and `hx-swap` explicitly. Never rely on htmx defaults
for non-trivial interactions — defaults change behavior in subtle ways.

### Alpine.js patterns

```html
<!-- Toggle panel -->
<div x-data="{ open: false }">
    <button @click="open = !open">Filters</button>
    <div x-show="open" x-cloak class="mt-2 p-4 border rounded">
        ...filters...
    </div>
</div>

<!-- Tab state -->
<div x-data="{ tab: 'details' }">
    <button :class="tab === 'details' ? 'border-b-2 border-blue-600' : ''" @click="tab = 'details'">Details</button>
    <button :class="tab === 'history' ? 'border-b-2 border-blue-600' : ''" @click="tab = 'history'">History</button>

    <div x-show="tab === 'details'">...</div>
    <div x-show="tab === 'history'">...</div>
</div>

<!-- Form character count (client-side only) -->
<div x-data="{ body: '' }">
    <textarea x-model="body" maxlength="500"></textarea>
    <p class="text-sm text-gray-500" x-text="`${500 - body.length} characters remaining`"></p>
</div>
```

Always add `x-cloak` to elements that should be hidden before Alpine initializes,
and include this in your CSS:

```css
[x-cloak] { display: none !important; }
```

### When to escalate to Svelte

Escalate a UI component to Svelte 5 when any of the following are true:

- The component manages complex local state with multiple interdependent values
  (e.g., a multi-step form wizard, a drag-and-drop interface, a real-time
  collaborative editor)
- The component requires reactive derived state that Alpine's `x-data` makes
  awkward or verbose
- The component will be reused across multiple pages or projects as a standalone unit
- The interactivity is rich enough that testing it as an isolated unit would
  be valuable

When escalating: propose the Svelte island boundary explicitly. The island is
embedded in a `templ` component via a `<div id="...">` mount point and a
`<script type="module">` that imports the compiled Svelte component.

---

## Tailwind CSS Conventions

Use Tailwind utility classes exclusively. Do not write custom CSS unless a
required visual effect genuinely cannot be expressed in utilities.

### Class ordering

Follow this order within a class attribute (matches Prettier plugin for Tailwind):

1. Layout (`flex`, `grid`, `block`, `hidden`)
2. Sizing (`w-`, `h-`, `max-w-`)
3. Spacing (`p-`, `m-`, `gap-`, `space-`)
4. Typography (`text-`, `font-`, `leading-`, `tracking-`)
5. Color (`bg-`, `text-`, `border-`)
6. Border (`border`, `rounded-`)
7. Effects (`shadow-`, `opacity-`)
8. Interactive (`hover:`, `focus:`, `active:`)
9. Responsive (`sm:`, `md:`, `lg:`)

### Reusable class groups

Extract repeated utility combinations into `@apply` blocks in a base CSS file
only when the combination appears in 5+ places. Name them semantically:

```css
.btn-primary {
    @apply inline-flex items-center px-4 py-2 bg-blue-600 text-white text-sm
           font-medium rounded-md hover:bg-blue-700 focus:outline-none
           focus:ring-2 focus:ring-blue-500 focus:ring-offset-2;
}
```

---

## Commenting Standards

Every function must have a comment. Comments are read by quality control agents
to verify that implementations match intent. Write them as contracts.

### Rules

1. **Every exported function:** Doc comment above the declaration.
2. **Every handler:** State what HTTP method/path it serves, what it returns,
   and how it handles htmx partial requests if applicable.
3. **Every templ component:** State what data it renders and when it is used
   (full page vs. partial swap).
4. **Every sqlc query:** Inline comment in the `.sql` file stating the query's
   purpose and any sentinel error behavior callers must handle.
5. **Non-obvious internal functions:** Brief comment stating why the function
   exists and what invariant it maintains.
6. **Skip comments only for:** Trivial getters, generated code (never edit
   sqlc output), and test helper functions under 5 lines.

### Comment format

```go
// FunctionName does X. It is called when Y.
// Returns Z on success, or W if the precondition is not met.
// Callers are responsible for V.
func FunctionName(...) { ... }
```

No `// TODO` comments in committed code unless accompanied by a GitHub issue number.
Use `// TODO(#42): ...` format.

---

## Step-by-Step Implementation Protocol

When implementing a feature, always follow this sequence:

**Step 0 — Clarify.** If the requirement is ambiguous, ask one targeted question
before doing anything. Do not ask multiple questions at once.

**Step 1 — Schema.** Show the SQL schema changes (new tables, columns, indexes).
Wait for confirmation.

**Step 2 — Queries.** Show the sqlc `.sql` query files. Wait for confirmation.

**Step 3 — Handlers.** Show the Go handler(s) with full comments. Wait for
confirmation.

**Step 4 — Templates.** Show the `.templ` components. Wait for confirmation.

**Step 5 — Wiring.** Show route registration and any dependency injection
changes. Wait for confirmation.

**Step 6 — Frontend polish.** Add Alpine.js state, htmx triggers, Tailwind
refinements if not already covered in templates. Wait for confirmation.

Each step is a potential commit boundary. If a step feels large, say so and
propose splitting it before writing code.

---

## Quality Control Checklist

Before presenting any code, verify:

- [ ] Every exported function has a doc comment
- [ ] Every handler comment states its HTTP method, path, and htmx behavior
- [ ] Every templ component comment states its purpose and usage context
- [ ] No raw SQL outside of `/queries/*.sql` files
- [ ] No third-party router or ORM added without explicit discussion
- [ ] htmx attributes include explicit `hx-target` and `hx-swap`
- [ ] Alpine.js used only for client-side UI state, not server interactions
- [ ] SQLite pragmas (WAL, foreign keys) configured at startup
- [ ] No package-level dependency variables — everything through `Handlers` struct

---

## Escalation Reference

| Situation | Action |
|---|---|
| Need concurrent multi-server DB access | Discuss Postgres upgrade |
| Need real-time push from server to client | Propose SSE or WebSocket, discuss tradeoffs |
| Alpine.js state becoming complex (3+ interdependent values) | Propose Svelte island |
| New Go dependency needed | State dependency, reason, and alternatives considered |
| Feature scope larger than estimated | Flag before writing, not after |

---

*Firefly Software stack agent · v1.0*
*Update this skill as conventions evolve. Version and date changes in git history.*