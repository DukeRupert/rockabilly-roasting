/* global React, RRPrim */
const { Eyebrow } = RRPrim;

const Story = () => (
  <section style={{
    background: 'var(--paper-warm)', padding: '90px 48px',
    borderTop: '2px solid var(--ink)', borderBottom: '2px solid var(--ink)',
    position: 'relative', overflow: 'hidden'
  }}>
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.2fr', gap: 60, alignItems: 'center', maxWidth: 1200, margin: '0 auto' }}>
      <div style={{ position: 'relative' }}>
        <div style={{
          border: '3px solid var(--ink)', padding: 10, background: 'var(--paper)',
          boxShadow: '6px 6px 0 0 var(--ink)', transform: 'rotate(-2deg)'
        }}>
          <div style={{
            aspectRatio: '4 / 5', background: 'var(--ink)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 30,
          }}>
            <img src="../../assets/double-candle.png" style={{ width: '100%', objectFit: 'contain' }}/>
          </div>
          <div style={{
            fontFamily: 'var(--font-mono)', fontSize: 13, textAlign: 'center',
            padding: '10px 0 4px', letterSpacing: '0.04em'
          }}>EST. RICHLAND · WA</div>
        </div>
      </div>

      <div>
        <Eyebrow>The Story</Eyebrow>
        <h2 style={{
          fontFamily: 'var(--font-heritage)', fontSize: 56, lineHeight: 1.05,
          textTransform: 'none', letterSpacing: '-0.01em',
          margin: '12px 0 18px'
        }}>
          A great bike. A rebel attitude. <span style={{ fontStyle: 'italic', color: 'var(--rust)' }}>And coffee you'd ride across town for.</span>
        </h2>
        <p style={{ fontFamily: 'var(--font-sans)', fontSize: 18, lineHeight: 1.6, color: 'var(--fg-2)', maxWidth: 540, margin: '0 0 14px' }}>
          We roast in small batches, we sell out most weeks, and we're okay with that.
          Every bag is dated. Every cup pulled like it matters — because it does.
        </p>
        <p style={{ fontFamily: 'var(--font-sans)', fontSize: 18, lineHeight: 1.6, color: 'var(--fg-2)', maxWidth: 540, margin: 0 }}>
          No bottomless venti nonsense. No latte art contests. Just honest coffee,
          straight-up, the way your grandad drank it — if your grandad rode a Triumph.
        </p>
        <div style={{ marginTop: 30, fontFamily: 'var(--font-script)', fontSize: 38, color: 'var(--rust)', lineHeight: 1 }}>
          — the crew
        </div>
      </div>
    </div>
  </section>
);
window.RRStory = Story;
