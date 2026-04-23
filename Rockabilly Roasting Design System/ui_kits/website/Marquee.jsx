/* global React */

const MARQUEE_ITEMS = [
  'ROASTED THIS WEEK', '★', 'RICHLAND · WA', '✦', 'SMALL BATCH', '★',
  'NO MIDDLEMAN', '✦', 'OPEN 6AM', '★', 'WE DON\'T SLEEP IN', '✦'
];

const Marquee = () => (
  <div style={{
    background: 'var(--amber)', color: 'var(--ink)',
    borderTop: '2px solid var(--ink)', borderBottom: '2px solid var(--ink)',
    padding: '14px 0', overflow: 'hidden', whiteSpace: 'nowrap'
  }}>
    <style>{`@keyframes rr-scroll { from { transform: translateX(0); } to { transform: translateX(-50%); } }`}</style>
    <div style={{
      display: 'inline-flex', gap: 48,
      animation: 'rr-scroll 30s linear infinite',
      fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 16,
      letterSpacing: '0.22em', textTransform: 'uppercase'
    }}>
      {[...MARQUEE_ITEMS, ...MARQUEE_ITEMS, ...MARQUEE_ITEMS, ...MARQUEE_ITEMS].map((t, i) => (
        <span key={i}>{t}</span>
      ))}
    </div>
  </div>
);
window.RRMarquee = Marquee;
