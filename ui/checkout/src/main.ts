import { mount } from 'svelte';
import App from './App.svelte';

function init() {
  const target = document.getElementById('checkout-app');
  if (!target) return;

  const cartId = target.dataset.cartId || '';
  const stripeKey = target.dataset.stripeKey || '';

  mount(App, {
    target,
    props: { cartId, stripeKey },
  });
}

// Module scripts are deferred, but ensure DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
