export interface CartItem {
  variant_id: string;
  product_title: string;
  sku: string;
  quantity: number;
  unit_price: number;
  line_total: number;
}

export interface CartResponse {
  cart_id: string;
  items: CartItem[];
  subtotal: number;
  currency: string;
}

export interface AddressRequest {
  email: string;
  first_name: string;
  last_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postal_code: string;
  country: string;
}

export interface AddressResponse {
  address_id: string;
  customer_id: string;
}

export interface PaymentIntentRequest {
  cart_id: string;
  address_id: string;
  customer_id: string;
}

export interface PaymentIntentResponse {
  client_secret: string;
  amount: number;
  currency: string;
  subtotal: number;
  discount_total: number;
  discount_name?: string;
  coupon_code?: string;
  tax_total: number;
  tax_label?: string;
  shipping_total: number;
  shipping_label?: string;
}

export interface ConfirmRequest {
  payment_intent_id: string;
  // The server ignores these now (the order is keyed by payment_intent_id and
  // was created at PI-creation time) but legacy clients may still send them.
  cart_id?: string;
  customer_id?: string;
  address_id?: string;
}

export interface ConfirmResponse {
  order_id: string;
  order_number: string;
  redirect: string;
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  const data = await res.json();
  if (!res.ok) {
    throw new Error(data.error || `Request failed: ${res.status}`);
  }
  return data as T;
}

export function getCart(): Promise<CartResponse> {
  return request<CartResponse>('/api/checkout/cart');
}

export function submitAddress(addr: AddressRequest): Promise<AddressResponse> {
  return request<AddressResponse>('/api/checkout/address', {
    method: 'POST',
    body: JSON.stringify(addr),
  });
}

export function createPaymentIntent(req: PaymentIntentRequest): Promise<PaymentIntentResponse> {
  return request<PaymentIntentResponse>('/api/checkout/payment-intent', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export function confirmOrder(req: ConfirmRequest): Promise<ConfirmResponse> {
  return request<ConfirmResponse>('/api/checkout/confirm', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}
