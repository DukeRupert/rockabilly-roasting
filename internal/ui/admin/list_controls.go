package admin

// Shared chrome for the admin list pages (orders, subscriptions). The classes
// live here rather than in one list's file because two pages reading the same
// way — stamped active tab, quiet pills — is the point; a copy in each file is
// how they drift apart.

// adminTabClass returns the class for a primary list tab. The active tab gets
// the paper-and-ink stamp treatment: ink border, hard offset shadow, cream
// surface — visually pasted on top of the row of inactive labels.
func adminTabClass(active bool) string {
	if active {
		return "relative shrink-0 whitespace-nowrap inline-flex items-center gap-2 border border-rr-border bg-rr-surface px-3.5 py-2 font-semibold text-rr-heading shadow-sm z-10"
	}
	return "shrink-0 whitespace-nowrap inline-flex items-center gap-2 border border-transparent px-3.5 py-2 text-rr-muted hover:text-rr-heading hover:bg-rr-raised transition-colors"
}

// adminTabCountClass returns the chip class beside each tab label. The active
// tab's count chip pops in rust to draw the eye to "you have N here";
// inactive chips sit quiet on raised paper.
func adminTabCountClass(active bool) string {
	if active {
		return "inline-flex items-center justify-center min-w-[1.25rem] px-1.5 py-px bg-rr-red text-white text-[0.65rem] font-bold tabular-nums tracking-wider"
	}
	return "inline-flex items-center justify-center min-w-[1.25rem] px-1.5 py-px bg-rr-raised text-rr-body text-[0.65rem] font-bold tabular-nums tracking-wider"
}

// adminPillClasses returns the class for a secondary filter pill — quieter
// than a tab, since these narrow within the tab rather than switching bucket.
func adminPillClasses(active bool) string {
	base := "inline-flex items-center rounded-sm border px-2.5 py-1 text-sm font-medium"
	if active {
		return base + " border-rr-heading bg-rr-heading text-rr-bg"
	}
	return base + " border-rr-border bg-rr-surface text-rr-body hover:bg-rr-raised"
}

// adminListPill is a value/label pair for a filter pill row.
type adminListPill struct {
	Value string
	Label string
}
