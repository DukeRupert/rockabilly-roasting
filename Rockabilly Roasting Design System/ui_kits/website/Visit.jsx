/* global React, RRPrim */
const { Button, Eyebrow, Chip } = RRPrim;

const Visit = () => (
  <section style={{ background: 'var(--paper)', padding: '80px 48px' }}>
    <div style={{
      maxWidth: 1100, margin: '0 auto',
      display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 40,
    }}>
      <div style={{
        background: 'var(--cream-hi)', border: '2px solid var(--ink)',
        boxShadow: '6px 6px 0 0 var(--ink)', padding: 36,
      }}>
        <Eyebrow>Come See Us</Eyebrow>
        <h2 style={{ fontFamily: 'var(--font-display)', fontSize: 54, lineHeight: 0.95, textTransform: 'uppercase', margin: '12px 0 18px' }}>
          THE SHOP
        </h2>
        <div style={{ fontFamily: 'var(--font-sans)', fontSize: 18, lineHeight: 1.7, color: 'var(--fg-1)' }}>
          1234 GEORGE WASHINGTON WAY<br/>
          RICHLAND · WA · 99354
        </div>
        <div style={{ margin: '22px 0', padding: '14px 0', borderTop: '2px solid var(--ink)', borderBottom: '2px solid var(--ink)' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontFamily: 'var(--font-sans)', fontSize: 15, letterSpacing: '0.06em', textTransform: 'uppercase', padding: '4px 0' }}>
            <span>Mon – Fri</span><span style={{ fontFamily: 'var(--font-mono)' }}>6:00A – 6:00P</span>
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontFamily: 'var(--font-sans)', fontSize: 15, letterSpacing: '0.06em', textTransform: 'uppercase', padding: '4px 0' }}>
            <span>Sat</span><span style={{ fontFamily: 'var(--font-mono)' }}>7:00A – 6:00P</span>
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontFamily: 'var(--font-sans)', fontSize: 15, letterSpacing: '0.06em', textTransform: 'uppercase', padding: '4px 0' }}>
            <span>Sun</span><span style={{ fontFamily: 'var(--font-mono)' }}>7:00A – 3:00P</span>
          </div>
        </div>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
          <Button variant="primary">Get Directions</Button>
          <Chip tone="amber">OPEN NOW</Chip>
        </div>
      </div>

      <div style={{
        background: 'var(--paper-warm)', border: '2px solid var(--ink)',
        boxShadow: '6px 6px 0 0 var(--ink)',
        backgroundImage: 'repeating-linear-gradient(45deg, rgba(14,13,12,0.05) 0 10px, transparent 10px 20px)',
        position: 'relative', minHeight: 380,
      }}>
        <div style={{
          position: 'absolute', top: '50%', left: '50%',
          transform: 'translate(-50%,-50%)', textAlign: 'center'
        }}>
          <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="var(--ink)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0Z"/>
            <circle cx="12" cy="10" r="3"/>
          </svg>
          <div style={{ fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 13, letterSpacing: '0.16em', textTransform: 'uppercase', marginTop: 8, color: 'var(--ink)' }}>
            Map · Richland, WA
          </div>
          <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--chrome-deep)', marginTop: 4 }}>
            [map placeholder]
          </div>
        </div>
      </div>
    </div>
  </section>
);
window.RRVisit = Visit;
