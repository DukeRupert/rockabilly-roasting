/* global React */

const Footer = () => (
  <footer style={{
    background: 'var(--ink)', color: 'var(--paper)',
    padding: '60px 48px 24px', borderTop: '3px solid var(--amber)',
  }}>
    <div style={{
      maxWidth: 1200, margin: '0 auto',
      display: 'grid', gridTemplateColumns: '1.4fr 1fr 1fr 1fr', gap: 48,
    }}>
      <div>
        <img src="../../assets/badge-white.png" alt="" style={{ width: 110, marginBottom: 16 }}/>
        <p style={{ fontFamily: 'var(--font-sans)', fontSize: 14, lineHeight: 1.6, color: 'var(--paper-warm)', margin: 0, maxWidth: 300 }}>
          Small-batch coffee, roasted with a rebel attitude in Richland, WA. Every bag dated. Every cup honest.
        </p>
      </div>

      {[
        { h: 'Shop', l: ['Coffees', 'Merch', 'Subscriptions', 'Gift Cards'] },
        { h: 'The Cafe', l: ['Menu', 'Hours', 'Events', 'Private Bookings'] },
        { h: 'Company', l: ['Our Story', 'Wholesale', 'Press', 'Contact'] },
      ].map(col => (
        <div key={col.h}>
          <div style={{ fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 12, letterSpacing: '0.2em', textTransform: 'uppercase', color: 'var(--amber)', marginBottom: 16 }}>
            {col.h}
          </div>
          {col.l.map(item => (
            <a key={item} href="#" style={{
              display: 'block', fontFamily: 'var(--font-sans)', fontSize: 15,
              letterSpacing: '0.04em', color: 'var(--paper)', textDecoration: 'none',
              padding: '4px 0'
            }}>{item}</a>
          ))}
        </div>
      ))}
    </div>

    <div style={{
      maxWidth: 1200, margin: '50px auto 0', paddingTop: 20,
      borderTop: '1px dashed rgba(246,239,225,0.3)',
      display: 'flex', justifyContent: 'space-between', alignItems: 'center'
    }}>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--chrome)', letterSpacing: '0.04em' }}>
        © 2026 ROCKABILLY ROASTING CO. · RICHLAND, WA · ALL RIGHTS RESERVED
      </div>
      <div style={{ fontFamily: 'var(--font-script)', fontSize: 24, color: 'var(--amber)' }}>
        Ride safe, drink dark.
      </div>
    </div>
  </footer>
);
window.RRFooter = Footer;
