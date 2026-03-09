<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Stripe, StripeElements } from '@stripe/stripe-js';
  import { getStripe, createElements } from '../lib/stripe';
  import { createPaymentIntent, confirmOrder, type CartResponse } from '../lib/api';
  import { formatCents } from '../lib/format';

  interface Props {
    cart: CartResponse;
    stripeKey: string;
    customerId: string;
    addressId: string;
    onBack: () => void;
  }

  let { cart, stripeKey, customerId, addressId, onBack }: Props = $props();

  let stripe = $state<Stripe | null>(null);
  let elements = $state<StripeElements | null>(null);
  let loading = $state(true);
  let processing = $state(false);
  let error = $state('');
  let clientSecret = $state('');

  onMount(async () => {
    try {
      stripe = await getStripe(stripeKey);
      if (!stripe) {
        error = 'Failed to load payment system';
        loading = false;
        return;
      }

      // Create PaymentIntent
      const piResponse = await createPaymentIntent({
        cart_id: cart.cart_id,
        address_id: addressId,
        customer_id: customerId,
      });

      clientSecret = piResponse.client_secret;

      // Ensure DOM is updated before mounting Stripe Element
      loading = false;
      await tick();

      // Mount Stripe Payment Element using a selector
      elements = createElements(stripe, clientSecret);
      const paymentElement = elements.create('payment');
      paymentElement.mount('#stripe-payment-element');
    } catch (e: any) {
      error = e.message || 'Failed to initialize payment';
      loading = false;
    }
  });

  async function handleSubmit(e: Event) {
    e.preventDefault();
    if (!stripe || !elements) return;

    processing = true;
    error = '';

    try {
      const { error: stripeError, paymentIntent } = await stripe.confirmPayment({
        elements,
        confirmParams: {
          return_url: window.location.origin + '/checkout',
        },
        redirect: 'if_required',
      });

      if (stripeError) {
        error = stripeError.message || 'Payment failed';
        processing = false;
        return;
      }

      if (!paymentIntent || paymentIntent.status !== 'succeeded') {
        error = 'Payment was not completed. Please try again.';
        processing = false;
        return;
      }

      const result = await confirmOrder({
        cart_id: cart.cart_id,
        customer_id: customerId,
        address_id: addressId,
        payment_intent_id: paymentIntent.id,
      });

      // Navigate to server-rendered confirmation page so it survives refresh.
      window.location.href = result.redirect;
    } catch (e: any) {
      error = e.message || 'Failed to complete order';
    } finally {
      processing = false;
    }
  }
</script>

<div>
  <h2 class="font-display text-2xl tracking-widest text-rr-heading mb-6">PAYMENT</h2>

  {#if error}
    <div class="mb-4 rounded-sm bg-rr-red/10 p-3 text-sm text-rr-red-lt">{error}</div>
  {/if}

  {#if loading}
    <div class="text-center py-8">
      <p class="text-rr-muted">Preparing payment...</p>
    </div>
  {:else}
    <form onsubmit={handleSubmit} class="space-y-6">
      <div id="stripe-payment-element"></div>

      <div class="flex items-center justify-between gap-4">
        <button
          type="button"
          onclick={onBack}
          disabled={processing}
          class="text-sm font-medium text-rr-amber hover:text-rr-amber-dark"
        >
          &larr; Back
        </button>

        <button
          type="submit"
          disabled={processing}
          class="btn rounded-sm bg-rr-red px-6 py-3 label-font text-sm text-white glow-red hover:bg-rr-red-lt disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {#if processing}
            Processing...
          {:else}
            Pay {formatCents(cart.subtotal)}
          {/if}
        </button>
      </div>
    </form>
  {/if}
</div>
