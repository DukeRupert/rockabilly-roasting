/* global React */
const { useState } = React;

const NAV = ['Coffee', 'Cafe', 'Story', 'Visit'];

const Header = () => {
  const [cart, setCart] = useState(2);
  return (
    <header style={{
      background: 'var(--paper)',
      borderBottom: '2px solid var(--ink)',
      padding: '14px 48px',
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      position: 'sticky', top: 0, zIndex: 10,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
        <img src="../../assets/logo-shield.png" alt="Rockabilly Roasting" style={{ height: 54 }}/>
        <div style={{
          fontFamily: 'var(--font-display)', fontSize: 18, textTransform: 'uppercase',
          lineHeight: 1, letterSpacing: '-0.005em'
        }}>
          ROCKABILLY<br/>ROASTING <span style={{ color: 'var(--rust)' }}>CO.</span>
        </div>
      </div>

      <nav style={{ display: 'flex', gap: 36 }}>
        {NAV.map(n => (
          <a key={n} href="#" style={{
            fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 13,
            letterSpacing: '0.18em', textTransform: 'uppercase',
            color: 'var(--ink)', textDecoration: 'none',
          }}>{n}</a>
        ))}
      </nav>

      <div style={{ display: 'flex', alignItems: 'center', gap: 18 }}>
        <a href="#" style={{
          fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 12,
          letterSpacing: '0.16em', textTransform: 'uppercase',
          color: 'var(--ink)', textDecoration: 'none',
        }}>LOG IN</a>
        <div style={{
          position: 'relative', padding: '8px 14px',
          border: '2px solid var(--ink)', background: 'var(--amber)',
          fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 12,
          letterSpacing: '0.14em', textTransform: 'uppercase',
          boxShadow: '3px 3px 0 0 var(--ink)', cursor: 'pointer',
        }}>
          CART · {cart}
        </div>
      </div>
    </header>
  );
};
window.RRHeader = Header;
