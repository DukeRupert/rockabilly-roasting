// Confirmation dialog for consequential admin actions.
//
// Replaces window.confirm() on the order actions. A submit button marked with
// ConfirmAttrs() (see internal/ui/admin/action_hint.templ) dispatches
// `admin-confirm` at the window carrying the form element and its copy; the
// single ConfirmDialogHost on the page catches it and shows the dialog.
//
// Confirming calls form.requestSubmit(), which fires a real submit event so
// htmx's hx-boost still handles the request and swaps the response in. The
// dialog is opened from the button's click, not the form's submit, so there is
// no loop back into it — see ConfirmAttrs for why the click is the hook.
document.addEventListener('alpine:init', () => {
  Alpine.data('adminConfirm', () => ({
    open: false,
    form: null,
    // Seeded so x-text has something to bind to before the first ask().
    copy: { title: '', lead: '', points: [], confirm: 'Confirm', danger: false },

    ask(event) {
      this.form = event.detail.form;
      this.copy = event.detail.copy;
      this.open = true;
      // Move focus into the dialog so keyboard and screen-reader users land on
      // the decision rather than being left behind on the button.
      this.$nextTick(() => this.$refs.confirm && this.$refs.confirm.focus());
    },

    close() {
      if (!this.open) return;
      this.open = false;
      // Return focus to the button that opened the dialog.
      const returnTo = this.form;
      this.form = null;
      if (returnTo) {
        const trigger = returnTo.querySelector('[type="submit"]');
        if (trigger) trigger.focus();
      }
    },

    go() {
      if (!this.form) return;
      const form = this.form;
      this.open = false;
      this.form = null;
      if (typeof form.requestSubmit === 'function') {
        form.requestSubmit();
      } else {
        form.submit();
      }
    },
  }));
});
