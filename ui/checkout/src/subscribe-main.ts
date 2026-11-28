import { mount } from 'svelte';
import SubscribeApp from './SubscribeApp.svelte';

function init() {
  const target = document.getElementById('subscribe-app');
  if (!target) return;

  const stripeKey = target.dataset.stripeKey || '';
  // The box, read once from the server-rendered page — never fetched on
  // mount. See storefront.SubscribeLineProps and its json tags for the shape.
  const lines = JSON.parse(target.dataset.lines || '[]');
  // A code a promo link left (see web/promo_code.go); empty when none.
  const coupon = target.dataset.coupon || '';

  mount(SubscribeApp, {
    target,
    props: { lines, stripeKey, coupon },
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
