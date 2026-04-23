/* global React, RRPrim */
const { Eyebrow } = RRPrim;

const SECTIONS = [
  {
    heading: 'The Espresso Bar',
    items: [
      ['Drip Coffee', 'bottomless', 3.25],
      ['Espresso', 'double shot', 4.00],
      ['Americano', '', 4.25],
      ['Cortado', '', 4.75],
      ['Cappuccino', '', 5.00],
      ['Latte', '', 5.25],
    ]
  },
  {
    heading: 'Cold & Slow',
    items: [
      ['Cold Brew', '16oz', 5.50],
      ['Nitro', 'on tap', 6.00],
      ['Iced Latte', '', 5.50],
      ['Shakerato', 'sweetened', 5.75],
    ]
  }
];

const MenuBoard = () => (
  <section style={{ background: 'var(--ink)', padding: '80px 48px', color: 'var(--paper)' }}>
    <div style={{ textAlign: 'center', marginBottom: 40 }}>
      <Eyebrow color="var(--amber)">The Cafe</Eyebrow>
      <h2 style={{ fontFamily: 'var(--font-display)', fontSize: 72, lineHeight: 1, textTransform: 'uppercase', margin: '10px 0 0', color: 'var(--paper)' }}>
        MENU
      </h2>
    </div>

    <div style={{ maxWidth: 960, margin: '0 auto', border: '2px solid var(--paper)', padding: 32, position: 'relative' }}>
      <div style={{ position: 'absolute', inset: 8, border: '1px solid var(--paper)', pointerEvents: 'none', opacity: 0.35 }}/>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 48, position: 'relative' }}>
        {SECTIONS.map(s => (
          <div key={s.heading}>
            <div style={{
              fontFamily: 'var(--font-script)', fontSize: 46, color: 'var(--amber)',
              lineHeight: 1, marginBottom: 14
            }}>{s.heading}</div>
            {s.items.map(([name, sub, price]) => (
              <div key={name} style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'baseline',
                gap: 16, padding: '8px 0', borderBottom: '1px dashed rgba(246,239,225,0.3)'
              }}>
                <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0, flex: 1 }}>
                  <span style={{ fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 17, textTransform: 'uppercase', letterSpacing: '0.06em', whiteSpace: 'nowrap' }}>{name}</span>
                  {sub && <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--chrome)', textTransform: 'lowercase', marginTop: 2 }}>{sub}</span>}
                </div>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 16, color: 'var(--amber)', flexShrink: 0 }}>{price.toFixed(2)}</span>
              </div>
            ))}
          </div>
        ))}
      </div>

      <div style={{ textAlign: 'center', marginTop: 28, fontFamily: 'var(--font-mono)', fontSize: 12, letterSpacing: '0.16em', textTransform: 'uppercase', color: 'var(--chrome)' }}>
        ★ ADD A SHOT · 1.00 ★ OAT / ALMOND · 0.75 ★ DECAF · FREE ★
      </div>
    </div>
  </section>
);
window.RRMenu = MenuBoard;
