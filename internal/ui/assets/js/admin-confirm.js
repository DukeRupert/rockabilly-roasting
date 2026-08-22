// Confirmation dialog for consequential admin actions.
//
// Replaces window.confirm() across the admin. A submit button marked with
// ConfirmAttrs() (see internal/ui/admin/action_hint.templ) dispatches
// `admin-confirm` at the window carrying what to do and the copy to show; the
// single ConfirmDialogHost on the page catches it and shows the dialog.
//
// The hook is the button's CLICK, never the form's submit event. The admin
// layout sets hx-boost on <body>, so htmx handles form submits itself and does
// NOT check whether another listener already cancelled the event — an
// @submit.prevent, or an onsubmit returning false, stops the browser's native
// submission while htmx fires the request anyway. That is how the fulfillment
// page's bulk confirms ended up decorative: staff clicked Cancel and the
// labels were bought regardless. Cancelling the click means no submit event is
// ever generated, which is the only reliable hook.
//
// Confirming calls form.requestSubmit() so hx-boost still handles the
// response, or runs a supplied callback for htmx-driven actions.
document.addEventListener('alpine:init', () => {
  Alpine.data('adminConfirm', () => ({
    open: false,
    form: null,
    run: null,
    cancelled: null,
    // Seeded so x-text has something to bind to before the first ask().
    copy: { title: '', lead: '', points: [], confirm: 'Confirm', danger: false },

    ask(event) {
      this.form = event.detail.form || null;
      this.run = event.detail.run || null;
      this.cancelled = event.detail.cancelled || null;
      this.copy = Object.assign(
        { title: '', lead: '', points: [], confirm: 'Confirm', danger: false },
        event.detail.copy || {},
      );
      this.open = true;
      // Move focus into the dialog so keyboard and screen-reader users land on
      // the decision rather than being left behind on the button.
      this.$nextTick(() => this.$refs.confirm && this.$refs.confirm.focus());
    },

    close() {
      if (!this.open) return;
      this.open = false;
      const returnTo = this.form;
      const cancelled = this.cancelled;
      this.form = null;
      this.run = null;
      this.cancelled = null;
      // Let the caller clean up — htmx needs to hear that it was declined.
      if (cancelled) cancelled();
      // Return focus to the button that opened the dialog.
      if (returnTo) {
        const trigger = returnTo.querySelector('[type="submit"]');
        if (trigger) trigger.focus();
      }
    },

    go() {
      const form = this.form;
      const run = this.run;
      this.open = false;
      this.form = null;
      this.run = null;
      this.cancelled = null;
      if (run) {
        run();
        return;
      }
      if (!form) return;
      if (typeof form.requestSubmit === 'function') {
        form.requestSubmit();
      } else {
        form.submit();
      }
    },
  }));
});

// hx-confirm support. htmx fires htmx:confirm before every request and calls
// window.confirm() itself when the element carries hx-confirm; preventing the
// event and calling issueRequest() later hands that decision to our dialog.
// An element may also carry data-confirm with richer copy, in which case
// hx-confirm is only the trigger and its text is unused.
document.addEventListener('htmx:confirm', (event) => {
  const question = event.detail.question;
  if (!question) return; // No hx-confirm on this element — nothing to ask.

  event.preventDefault();

  const el = event.detail.elt;
  let copy = { title: question, lead: '', points: [], confirm: 'Confirm', danger: true };
  if (el && el.dataset && el.dataset.confirm) {
    try {
      copy = JSON.parse(el.dataset.confirm);
    } catch (err) {
      // Fall back to the plain hx-confirm question rather than dropping the
      // confirmation altogether.
    }
  }

  // Declining means simply never calling issueRequest — htmx drops the request
  // when the event is prevented and nothing resumes it. Do NOT call
  // issueRequest(false) to decline: the argument is "skip the confirmation",
  // so false makes htmx ask again with its own blocking window.confirm().
  window.dispatchEvent(
    new CustomEvent('admin-confirm', {
      detail: {
        copy: copy,
        run: () => event.detail.issueRequest(true),
      },
    }),
  );
});
