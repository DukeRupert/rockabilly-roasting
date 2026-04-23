/* global React, RRPrim */
const { Eyebrow, Chip, Button } = RRPrim;

const COFFEES = [
  { name: 'GREASY SPOON', tag: 'DARK ROAST', notes: 'Cocoa · Cedar · A kick at the back.', price: 18, chip: 'House', tone: 'amber' },
  { name: 'SWITCHBLADE', tag: 'SINGLE ORIGIN · ETHIOPIA', notes: 'Blueberry · Jasmine · Honey.', price: 22, chip: 'New', tone: 'rust' },
  { name: 'CHROME', tag: 'MEDIUM ROAST', notes: 'Brown sugar · Orange peel · Clean finish.', price: 19, chip: 'Staff Pick', tone: 'ink' },
  { name: 'MIDNIGHT RIDE', tag: 'DECAF · COLOMBIA', notes: 'Dark chocolate · Toasted walnut.', price: 20, chip: 'Decaf', tone: 'paper' },
];

const Card = ({ c }) => (
  <div style={{
    background: 'var(--cream-hi)', border: '2px solid var(--ink)',
    boxShadow: '4px 4px 0 0 var(--ink)', padding: 22,
    display: 'flex', flexDirection: 'column', gap: 10,
  }}>
    <div style={{
      aspectRatio: '1 / 1', background: 'var(--paper-warm)',
      border: '2px solid var(--ink)', display: 'flex',
      alignItems: 'center', justifyContent: 'center', marginBottom: 4,
    }}>
      <img src="../../assets/logo-shield.png" style={{ width: '55%', opacity: 0.82 }}/>
    </div>
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
      <span style={{ fontFamily: 'var(--font-sans)', fontSize: 11, letterSpacing: '0.16em', textTransform: 'uppercase', color: 'var(--chrome-deep)' }}>{c.tag}</span>
      <Chip tone={c.tone} style={{ fontSize: 10, padding: '3px 8px' }}>{c.chip}</Chip>
    </div>
    <div style={{ fontFamily: 'var(--font-display)', fontSize: 28, lineHeight: 0.95, textTransform: 'uppercase' }}>{c.name}</div>
    <p style={{ fontFamily: 'var(--font-sans)', fontSize: 14, color: 'var(--fg-2)', margin: 0, lineHeight: 1.45 }}>{c.notes}</p>
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 6, paddingTop: 12, borderTop: '2px solid var(--ink)' }}>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 16 }}>${c.price}.00 · 12oz</span>
      <span style={{
        fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 11,
        letterSpacing: '0.14em', textTransform: 'uppercase',
        background: 'var(--rust)', color: 'var(--paper)',
        padding: '6px 10px', border: '2px solid var(--ink)', cursor: 'pointer',
      }}>ADD</span>
    </div>
  </div>
);

const Coffees = () => (
  <section style={{ background: 'var(--paper)', padding: '80px 48px' }}>
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', marginBottom: 32 }}>
      <div>
        <Eyebrow>Roasted 04.18.26</Eyebrow>
        <h2 style={{ fontFamily: 'var(--font-display)', fontSize: 64, lineHeight: 0.95, textTransform: 'uppercase', margin: '10px 0 0' }}>
          THIS WEEK'S<br/>
          <span style={{ fontFamily: 'var(--font-script)', color: 'var(--rust)', textTransform: 'none', fontSize: 72, letterSpacing: 0 }}>fresh</span>{' '}
          ROAST
        </h2>
      </div>
      <Button variant="ghost">All Coffees ›</Button>
    </div>
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 24 }}>
      {COFFEES.map(c => <Card key={c.name} c={c}/>)}
    </div>
  </section>
);
window.RRCoffees = Coffees;
