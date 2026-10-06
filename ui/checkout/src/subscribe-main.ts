import { mount } from 'svelte';
import SubscribeApp from './SubscribeApp.svelte';

function init() {
  const target = document.getElementById('subscribe-app');
  if (!target) return;

  const stripeKey = target.dataset.stripeKey || '';
  // The box, read once from the server-rendered page — never fetched on
  // mount. See storefront.SubscribeLineProps and its json tags for the shape.
  const lines = JSON.parse(target.dataset.lines || '[]');

  mount(SubscribeApp, {
    target,
    props: { lines, stripeKey },
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
