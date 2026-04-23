/* global React, RRPrim */
const { Button, Eyebrow, Stamp } = RRPrim;

const Hero = () => (
  <section style={{
    background: 'var(--ink)', color: 'var(--paper)',
    padding: '80px 48px 100px', position: 'relative', overflow: 'hidden'
  }}>
    {/* halftone dot pattern */}
    <div style={{
      position: 'absolute', inset: 0, pointerEvents: 'none',
      backgroundImage: 'radial-gradient(rgba(246,239,225,0.06) 1.5px, transparent 1.5px)',
      backgroundSize: '14px 14px', maskImage: 'linear-gradient(180deg, black, transparent 80%)'
    }}/>

    <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr', gap: 60, alignItems: 'center', position: 'relative' }}>
      <div>
        <Eyebrow color="var(--amber)">Richland, WA · Since forever</Eyebrow>
        <h1 style={{
          fontFamily: 'var(--font-display)', fontSize: 'clamp(72px, 9vw, 140px)',
          lineHeight: 0.92, textTransform: 'uppercase', letterSpacing: '-0.01em',
          margin: '16px 0 12px', color: 'var(--paper)'
        }}>
          BURN BOTH<br/>
          <span style={{ color: 'var(--amber)' }}>ENDS.</span><br/>
          ROAST THE<br/>BEANS.
        </h1>
        <p style={{
          fontFamily: 'var(--font-sans)', fontSize: 18, lineHeight: 1.55,
          color: 'var(--paper-warm)', maxWidth: 520, margin: '18px 0 32px'
        }}>
          Small-batch coffee, roasted in Richland and poured without fuss.
          No warehouse. No middleman. Beans come in green, leave as coffee —
          and they don't sit around.
        </p>
        <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
          <Button variant="primary">Shop This Week's Roast</Button>
          <Button variant="ghost" style={{ borderColor: 'var(--paper)', color: 'var(--paper)' }}>
            Visit the Cafe ›
          </Button>
        </div>
      </div>

      <div style={{ position: 'relative', display: 'flex', justifyContent: 'center' }}>
        <img src="../../assets/double-candle.png" alt="" style={{ width: '100%', maxWidth: 440, filter: 'drop-shadow(0 0 60px rgba(242,160,61,0.25))' }}/>
        <div style={{ position: 'absolute', top: 10, right: -10 }}>
          <Stamp rotate={8} color="var(--amber)">Fresh Weekly</Stamp>
        </div>
      </div>
    </div>
  </section>
);
window.RRHero = Hero;
