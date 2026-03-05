import { mount } from 'svelte';
import SubscribeApp from './SubscribeApp.svelte';

function init() {
  const target = document.getElementById('subscribe-app');
  if (!target) return;

  const planId = target.dataset.planId || '';
  const planName = target.dataset.planName || '';
  const price = parseInt(target.dataset.price || '0', 10);
  const interval = target.dataset.interval || '';
  const stripeKey = target.dataset.stripeKey || '';

  mount(SubscribeApp, {
    target,
    props: { planId, planName, price, interval, stripeKey },
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
