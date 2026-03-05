<script lang="ts">
  import { getCart, type CartResponse } from './lib/api';
  import Information from './steps/Information.svelte';
  import Payment from './steps/Payment.svelte';
  import Confirmation from './steps/Confirmation.svelte';
  import { formatCents } from './lib/format';

  interface Props {
    cartId: string;
    stripeKey: string;
  }

  let { cartId, stripeKey }: Props = $props();

  type Step = 'information' | 'payment' | 'confirmation';

  let step = $state<Step>('information');
  let cart = $state<CartResponse | null>(null);
  let loading = $state(true);
  let error = $state('');

  // Checkout state carried between steps
  let customerId = $state('');
  let addressId = $state('');
  let orderNumber = $state('');
  let orderId = $state('');

  const steps: { key: Step; label: string }[] = [
    { key: 'information', label: 'Information' },
    { key: 'payment', label: 'Payment' },
    { key: 'confirmation', label: 'Confirmation' },
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

  function handlePaymentComplete(e: { orderNumber: string; orderId: string }) {
    orderNumber = e.orderNumber;
    orderId = e.orderId;
    step = 'confirmation';
  }
</script>

<div class="max-w-2xl mx-auto">
  <!-- Step indicator -->
  <nav class="mb-8">
    <ol class="flex items-center gap-2 text-sm">
      {#each steps as s, i}
        {#if i > 0}
          <li class="text-stone-300">/</li>
        {/if}
        <li
          class={i === currentStepIndex
            ? 'font-semibold text-hiri-teal'
            : i < currentStepIndex
              ? 'text-hiri-text'
              : 'text-stone-400'}
        >
          {s.label}
        </li>
      {/each}
    </ol>
  </nav>

  {#if loading}
    <div class="text-center py-12">
      <p class="text-stone-500">Loading checkout...</p>
    </div>
  {:else if error && !cart?.items.length}
    <div class="text-center py-12">
      <p class="text-stone-500 mb-4">{error}</p>
      <a href="/cart" class="text-hiri-teal hover:text-hiri-teal-dark font-medium">Return to cart</a
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
            onComplete={handlePaymentComplete}
            onBack={() => (step = 'information')}
          />
        {:else if step === 'confirmation'}
          <Confirmation {orderNumber} />
        {/if}
      </div>

      <!-- Order summary sidebar -->
      {#if step !== 'confirmation'}
        <div class="mt-8 lg:mt-0 lg:col-span-2">
          <div class="rounded-lg bg-stone-50 p-6">
            <h2 class="text-lg font-semibold text-stone-900 mb-4">Order summary</h2>
            <ul class="divide-y divide-stone-200">
              {#each cart.items as item}
                <li class="py-3 flex justify-between text-sm">
                  <div>
                    <p class="font-medium text-stone-900">{item.product_title}</p>
                    <p class="text-stone-500">
                      {item.sku} &times; {item.quantity}
                    </p>
                  </div>
                  <p class="font-medium text-stone-900">{formatCents(item.line_total)}</p>
                </li>
              {/each}
            </ul>
            <div class="mt-4 border-t border-stone-200 pt-4">
              <div class="flex justify-between text-sm">
                <p class="text-stone-500">Subtotal</p>
                <p class="font-medium text-stone-900">{formatCents(cart.subtotal)}</p>
              </div>
              <div class="flex justify-between text-sm mt-1">
                <p class="text-stone-500">Shipping</p>
                <p class="text-stone-500">Free</p>
              </div>
              <div class="flex justify-between text-base font-semibold mt-3 pt-3 border-t border-stone-200">
                <p>Total</p>
                <p>{formatCents(cart.subtotal)}</p>
              </div>
            </div>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>
