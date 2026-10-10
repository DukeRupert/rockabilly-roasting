// IntentSequencer creates payment intents one at a time, for whatever the
// page describes when each create goes out, and makes sure every intent it
// abandons is handed to a later create to cancel.
//
// Two creates racing would let whichever answered last decide what the card
// is charged for — possibly a box from before an edit — and the earlier
// intent would never be cancelled. So a change that lands while a create is
// in flight only marks it stale. When a stale create answers:
//
//   - with an intent: that intent is already the wrong charge. It becomes the
//     previous intent, and a fresh create goes out for the current page.
//   - with an error: the error is about a box or address no longer on
//     screen, so it is not reported. A fresh create goes out for the current
//     page; its answer is the one that counts.
//
// Either way, if the page cannot be charged for right now (the form went
// invalid), the sequencer goes idle instead, and the next request carries
// the abandoned intent's id.
//
// A create that answers while still current is final: its intent is ready,
// or its error is reported. There is no blind retry — a stale answer is
// followed by another create only because the page changed, so the loop is
// bounded by the customer's own edits.
//
// A create that fails never sent its cancellation (the server cancels the
// previous intent only after the new one exists), so the previous id is kept
// and goes out again with the next create.
//
// Plain TypeScript, no Svelte, and nothing Node's type stripping refuses
// (no parameter properties or enums): intent-sequencer.test.ts runs it under
// `node --test` with no test dependency.

export type RequestOutcome<T> =
  // The intent for the page as it is now.
  | { kind: 'ready'; result: T }
  // This create answered for the page as it is now, and failed.
  | { kind: 'failed'; error: unknown }
  // A create was already in flight; it was marked stale and the caller that
  // started it will receive the outcome for the current page.
  | { kind: 'superseded' }
  // The page changed mid-create and cannot be charged for yet. Nothing is
  // in flight; the next request() cancels whatever was abandoned.
  | { kind: 'idle' };

export interface IntentSequencerOptions<T> {
  // Sends one create for the page as it is at the moment of the call,
  // cancelling previousId (if any) once the new intent exists.
  create(previousId: string | undefined): Promise<T>;
  // The intent id inside a create's result.
  idOf(result: T): string;
  // Whether the page can be charged for right now (the form is valid).
  canCreate(): boolean;
}

export class IntentSequencer<T> {
  private inFlight = false;
  private stale = false;
  private previousId = '';
  private readonly opts: IntentSequencerOptions<T>;

  constructor(opts: IntentSequencerOptions<T>) {
    this.opts = opts;
  }

  // abandon remembers an intent the page will no longer use, and marks any
  // create in flight stale. The abandoned id goes out with the next create.
  abandon(id?: string): void {
    if (id) this.previousId = id;
    if (this.inFlight) this.stale = true;
  }

  // pendingCancel is the id the next create will cancel, if any.
  get pendingCancel(): string {
    return this.previousId;
  }

  get busy(): boolean {
    return this.inFlight;
  }

  async request(): Promise<RequestOutcome<T>> {
    if (this.inFlight) {
      this.stale = true;
      return { kind: 'superseded' };
    }
    this.inFlight = true;
    try {
      for (;;) {
        this.stale = false;
        let result: T;
        try {
          result = await this.opts.create(this.previousId || undefined);
        } catch (error) {
          // previousId is kept: a failed create cancelled nothing.
          if (!this.stale) return { kind: 'failed', error };
          if (!this.opts.canCreate()) return { kind: 'idle' };
          continue;
        }
        this.previousId = '';
        if (!this.stale) return { kind: 'ready', result };
        this.previousId = this.opts.idOf(result);
        if (!this.opts.canCreate()) return { kind: 'idle' };
      }
    } finally {
      this.inFlight = false;
    }
  }
}

// intentIdOf reads a PaymentIntent's id out of its client secret
// (`pi_123_secret_abc` → `pi_123`).
export function intentIdOf(clientSecret: string): string {
  return clientSecret.split('_secret')[0];
}
