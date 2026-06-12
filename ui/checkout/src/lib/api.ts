export interface CartItem {
  variant_id: string;
  product_title: string;
  // Human-readable option summary ("Whole Bean · 12oz"); absent for
  // single-variant products. Preferred over the SKU for display.
  variant_label?: string;
  thumbnail_url?: string;
  sku: string;
  quantity: number;
  unit_price: number;
  line_total: number;
}

// CheckoutPrefill mirrors the server's prefill payload: contact info and
// default address for a signed-in customer. All fields may be empty strings.
export interface CheckoutPrefill {
  email: string;
  phone?: string;
  first_name?: string;
  last_name?: string;
  line1?: string;
  line2?: string;
  city?: string;
  state?: string;
  postal_code?: string;
  country?: string;
}

// AppliedCoupon describes a coupon already attached to the cart and still
// valid — used to restore the applied state after a reload.
export interface AppliedCoupon {
  code: string;
  name: string;
}

export interface CartResponse {
  cart_id: string;
  items: CartItem[];
  subtotal: number;
  currency: string;
  // Present only when the visitor has a customer session.
  prefill?: CheckoutPrefill;
  coupon?: AppliedCoupon;
}

export interface AddressRequest {
  email: string;
  phone?: string;
  first_name: string;
  last_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postal_code: string;
  country: string;
}

export type LocalFulfillmentMethod = 'local_delivery' | 'pickup';

export interface AddressResponse {
  address_id: string;
  customer_id: string;
  eligible_local_methods: LocalFulfillmentMethod[];
  local_pickup_instructions?: string;
  local_delivery_days?: string;
  preferred_local_fulfillment?: LocalFulfillmentMethod;
}

export interface PaymentIntentRequest {
  cart_id: string;
  address_id: string;
  customer_id: string;
  shipping_method?: LocalFulfillmentMethod | '';
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

// ApiError carries the full shape of a failed checkout API response so callers
// can react beyond the message string: `errors` is the per-field validation map
// the address endpoint returns on 422, and `code` is a stable machine tag (e.g.
// "address_incomplete") the payment step uses to route the buyer back to the
// step that can fix the problem.
export class ApiError extends Error {
  status: number;
  code?: string;
  errors?: Record<string, string>;

  constructor(
    message: string,
    status: number,
    code?: string,
    errors?: Record<string, string>,
  ) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.errors = errors;
  }
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });

  let data: any = null;
  try {
    data = await res.json();
  } catch {
    // Non-JSON body (e.g. a proxy/gateway error page). Fall through to a
    // generic message keyed off the status code below.
  }

  if (!res.ok) {
    const message =
      (data && data.error) ||
      `Something went wrong (error ${res.status}). Please try again.`;
    throw new ApiError(message, res.status, data?.code, data?.errors);
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

export interface ApplyCouponResponse {
  valid: boolean;
  discount_name?: string;
  discount_type?: string;
  discount_value?: number;
  error_message?: string;
}

export function applyCoupon(cartId: string, code: string): Promise<ApplyCouponResponse> {
  return request<ApplyCouponResponse>('/api/checkout/coupon', {
    method: 'POST',
    body: JSON.stringify({ cart_id: cartId, code }),
  });
}

export function removeCoupon(cartId: string): Promise<void> {
  return request<void>('/api/checkout/coupon', {
    method: 'DELETE',
    body: JSON.stringify({ cart_id: cartId }),
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
