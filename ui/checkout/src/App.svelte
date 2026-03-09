<script lang="ts">
  import { getCart, type CartResponse } from './lib/api';
  import Information from './steps/Information.svelte';
  import Payment from './steps/Payment.svelte';
  import { formatCents } from './lib/format';

  interface Props {
    cartId: string;
    stripeKey: string;
  }

  let { cartId, stripeKey }: Props = $props();

  type Step = 'information' | 'payment';

  let step = $state<Step>('information');
  let cart = $state<CartResponse | null>(null);
  let loading = $state(true);
  let error = $state('');

  // Checkout state carried between steps
  let customerId = $state('');
  let addressId = $state('');

  const steps: { key: Step; label: string }[] = [
    { key: 'information', label: 'Information' },
    { key: 'payment', label: 'Payment' },
  ];

  let currentStepIndex = $derived(steps.findIndex((s) => s.key === step));

  $effect(() => {
    loadCart();
  });

  async function loadCart() {
    try {
      loading = true;
      error = '';
      cart = await getCart();
      if (!cart.items.length) {
        error = 'Your cart is empty.';
      }
    } catch (e: any) {
      error = e.message || 'Failed to load cart';
    } finally {
      loading = false;
    }
  }

  function handleAddressComplete(e: { customerId: string; addressId: string }) {
    customerId = e.customerId;
    addressId = e.addressId;
    step = 'payment';
  }

</script>

<div class="max-w-2xl mx-auto">
  <!-- Step indicator -->
  <nav class="mb-8">
    <ol class="flex items-center gap-2 text-sm">
      {#each steps as s, i}
        {#if i > 0}
          <li class="text-rr-border">/</li>
        {/if}
        <li
          class={i === currentStepIndex
            ? 'font-semibold text-rr-red'
            : i < currentStepIndex
              ? 'text-rr-heading'
              : 'text-rr-faint'}
        >
          {s.label}
        </li>
      {/each}
    </ol>
  </nav>

  {#if loading}
    <div class="text-center py-12">
      <p class="text-rr-muted">Loading checkout...</p>
    </div>
  {:else if error && !cart?.items.length}
    <div class="text-center py-12">
      <p class="text-rr-muted mb-4">{error}</p>
      <a href="/cart" class="text-rr-red hover:text-rr-red-lt font-medium">Return to cart</a
      >
    </div>
  {:else if cart}
    <div class="lg:grid lg:grid-cols-5 lg:gap-x-8">
      <!-- Main content -->
      <div class="lg:col-span-3">
        {#if step === 'information'}
          <Information {cart} onComplete={handleAddressComplete} />
        {:else if step === 'payment'}
          <Payment
            {cart}
            {stripeKey}
            {customerId}
            {addressId}
            onBack={() => (step = 'information')}
          />
        {/if}
      </div>

      <!-- Order summary sidebar -->
        <div class="mt-8 lg:mt-0 lg:col-span-2">
          <div class="rounded-sm bg-rr-surface border border-rr-border p-6">
            <h2 class="font-display text-xl tracking-widest text-rr-heading mb-4">ORDER SUMMARY</h2>
            <ul class="divide-y divide-rr-border">
              {#each cart.items as item}
                <li class="py-3 flex justify-between text-sm font-body">
                  <div>
                    <p class="font-medium text-rr-heading">{item.product_title}</p>
                    <p class="text-rr-muted">
                      {item.sku} &times; {item.quantity}
                    </p>
                  </div>
                  <p class="font-medium text-rr-heading">{formatCents(item.line_total)}</p>
                </li>
              {/each}
            </ul>
            <div class="mt-4 border-t border-rr-border pt-4">
              <div class="flex justify-between text-sm">
                <p class="text-rr-muted">Subtotal</p>
                <p class="font-medium text-rr-heading">{formatCents(cart.subtotal)}</p>
              </div>
              <div class="flex justify-between text-sm mt-1">
                <p class="text-rr-muted">Shipping</p>
                <p class="text-rr-muted">Free</p>
              </div>
              <div class="flex justify-between text-base font-semibold mt-3 pt-3 border-t border-rr-border">
                <p>Total</p>
                <p>{formatCents(cart.subtotal)}</p>
              </div>
            </div>
          </div>
        </div>
    </div>
  {/if}
</div>
