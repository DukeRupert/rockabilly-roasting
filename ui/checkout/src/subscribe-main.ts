import { mount } from 'svelte';
import SubscribeApp from './SubscribeApp.svelte';

function init() {
  const target = document.getElementById('subscribe-app');
  if (!target) return;

  const planId = target.dataset.planId || '';
  const variantId = target.dataset.variantId || '';
  const quantity = parseInt(target.dataset.quantity || '1', 10);
  const planName = target.dataset.planName || '';
  const price = parseInt(target.dataset.price || '0', 10);
  const interval = target.dataset.interval || '';
  const stripeKey = target.dataset.stripeKey || '';

  mount(SubscribeApp, {
    target,
    props: { planId, variantId, quantity, planName, price, interval, stripeKey },
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
