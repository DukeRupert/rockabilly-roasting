// Run with `node --test src/` from ui/checkout (Node 23.6+ strips the types).
// The page is a box version and a valid flag; the server hands out intents
// that the test answers by hand, so every interleaving is spelled out.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IntentSequencer, type RequestOutcome } from './intent-sequencer.ts';

interface Intent {
  id: string;
  box: number;
}

interface Call {
  previousId: string | undefined;
  box: number;
  resolve(): void;
  reject(err: Error): void;
}

function harness() {
  const page = { box: 1, valid: true };
  const calls: Call[] = [];
  const created: Intent[] = [];
  const cancelled = new Set<string>();
  let next = 1;

  const seq = new IntentSequencer<Intent>({
    create(previousId) {
      const box = page.box;
      return new Promise<Intent>((resolve, reject) => {
        calls.push({
          previousId,
          box,
          // The server cancels the previous intent only once the new one
          // exists, as handleSubscribePaymentIntent does.
          resolve() {
            const intent = { id: `pi_${next++}`, box };
            created.push(intent);
            if (previousId) cancelled.add(previousId);
            resolve(intent);
          },
          reject,
        });
      });
    },
    idOf: (i) => i.id,
    canCreate: () => page.valid,
  });

  // inFlight is how many creates have been sent and not answered.
  let answered = 0;
  const flush = () => new Promise((r) => setImmediate(r));
  return {
    page,
    calls,
    created,
    cancelled,
    seq,
    async answer(i: number, err?: Error) {
      const c = calls[i];
      if (err) c.reject(err);
      else c.resolve();
      answered++;
      await flush();
    },
    inFlight: () => calls.length - answered,
    // Every intent ever created is either the live one, cancelled, or
    // queued for the next create to cancel.
    assertNoneLeaked(live?: string) {
      for (const i of created) {
        if (i.id === live) continue;
        assert.ok(
          cancelled.has(i.id) || seq.pendingCancel === i.id,
          `${i.id} was abandoned and never cancelled`,
        );
      }
    },
    flush,
  };
}

test('a create with no change in flight is ready', async () => {
  const h = harness();
  const p = h.seq.request();
  await h.answer(0);
  const out = await p;
  assert.deepEqual(out, { kind: 'ready', result: { id: 'pi_1', box: 1 } });
  h.assertNoneLeaked('pi_1');
});

test('a second request while one is in flight is superseded, never a second create', async () => {
  const h = harness();
  const first = h.seq.request();
  h.page.box = 2;
  const second = await h.seq.request();
  assert.equal(second.kind, 'superseded');
  assert.equal(h.inFlight(), 1);

  // The stale answer is handed to a fresh create for box 2.
  await h.answer(0);
  assert.equal(h.calls.length, 2);
  assert.equal(h.calls[1].previousId, 'pi_1');
  assert.equal(h.calls[1].box, 2);

  await h.answer(1);
  const out = await first;
  assert.equal(out.kind, 'ready');
  assert.equal((out as { result: Intent }).result.box, 2);
  h.assertNoneLeaked('pi_2');
  assert.ok(h.cancelled.has('pi_1'));
});

test('a stale create that fails is not reported; the current box is created', async () => {
  const h = harness();
  const first = h.seq.request();
  h.page.box = 2;
  h.seq.abandon();

  await h.answer(0, new Error('failed to prepare payment'));
  assert.equal(h.calls.length, 2, 'a create goes out for the current box');
  assert.equal(h.calls[1].box, 2);

  await h.answer(1);
  const out = await first;
  assert.equal(out.kind, 'ready');
  assert.equal((out as { result: Intent }).result.box, 2);
});

test('when the create for the current box also fails, that error is reported', async () => {
  const h = harness();
  const first = h.seq.request();
  h.page.box = 2;
  h.seq.abandon();

  await h.answer(0, new Error('old box'));
  await h.answer(1, new Error('new box'));
  const out = await first;
  assert.equal(out.kind, 'failed');
  assert.equal((out as { error: Error }).error.message, 'new box');
  assert.equal(h.calls.length, 2, 'no blind retry');
  assert.equal(h.seq.busy, false);
});

test('a failed create keeps the abandoned id, and the next create cancels it', async () => {
  const h = harness();
  // Live intent pi_1 for box 1.
  const p1 = h.seq.request();
  await h.answer(0);
  await p1;

  // The box changes: pi_1 is abandoned and the next create fails.
  h.page.box = 2;
  h.seq.abandon('pi_1');
  const p2 = h.seq.request();
  assert.equal(h.calls[1].previousId, 'pi_1');
  await h.answer(1, new Error('nope'));
  assert.equal((await p2).kind, 'failed');
  assert.equal(h.seq.pendingCancel, 'pi_1');
  h.assertNoneLeaked();

  // The retry carries it again.
  const p3 = h.seq.request();
  assert.equal(h.calls[2].previousId, 'pi_1');
  await h.answer(2);
  assert.equal((await p3).kind, 'ready');
  assert.ok(h.cancelled.has('pi_1'));
  assert.equal(h.seq.pendingCancel, '');
});

test('a stale answer when the form went invalid goes idle, and the next create cancels it', async () => {
  const h = harness();
  const first = h.seq.request();
  h.page.valid = false;
  h.seq.abandon();
  await h.answer(0);
  assert.equal((await first).kind, 'idle');
  assert.equal(h.calls.length, 1, 'nothing to create for an invalid form');
  assert.equal(h.seq.pendingCancel, 'pi_1');
  h.assertNoneLeaked();

  h.page.valid = true;
  h.page.box = 2;
  const again = h.seq.request();
  assert.equal(h.calls[1].previousId, 'pi_1');
  await h.answer(1);
  assert.equal((await again).kind, 'ready');
  h.assertNoneLeaked('pi_2');
});

test('a stale failure when the form went invalid goes idle without an error', async () => {
  const h = harness();
  const first = h.seq.request();
  h.page.valid = false;
  h.seq.abandon();
  await h.answer(0, new Error('old box'));
  assert.equal((await first).kind, 'idle');
  assert.equal(h.seq.busy, false);
});

test('edits landing during every create keep exactly one create in flight and cancel every intent but the last', async () => {
  const h = harness();
  const first = h.seq.request();
  for (let i = 0; i < 5; i++) {
    h.page.box++;
    assert.equal((await h.seq.request()).kind, 'superseded');
    assert.equal(h.inFlight(), 1);
    // Every other stale create fails, so both kinds of answer are mixed in.
    await h.answer(i, i % 2 ? new Error('flaky') : undefined);
    assert.equal(h.inFlight(), 1);
  }
  await h.answer(5);
  const out = await first;
  assert.equal(out.kind, 'ready');
  const live = (out as { result: Intent }).result;
  assert.equal(live.box, h.page.box, 'the card is charged for the box on screen');
  h.assertNoneLeaked(live.id);
});

test('an abandon with nothing in flight only queues the id', async () => {
  const h = harness();
  h.seq.abandon('pi_old');
  assert.equal(h.calls.length, 0);
  const p = h.seq.request();
  assert.equal(h.calls[0].previousId, 'pi_old');
  await h.answer(0);
  assert.equal((await p).kind, 'ready');
  assert.ok(h.cancelled.has('pi_old'));
});
