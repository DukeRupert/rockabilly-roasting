/* global React */

const { useState } = React;

const Eyebrow = ({ children, color = 'var(--chrome-deep)', style = {} }) => (
  <div style={{
    fontFamily: 'var(--font-sans)', fontWeight: 600, fontSize: 12,
    letterSpacing: '0.24em', textTransform: 'uppercase', color, ...style
  }}>{children}</div>
);

const Button = ({ variant = 'primary', children, onClick, style = {} }) => {
  const [hover, setHover] = useState(false);
  const [press, setPress] = useState(false);
  const base = {
    fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 13,
    letterSpacing: '0.14em', textTransform: 'uppercase',
    padding: '13px 24px', border: '2px solid var(--ink)',
    borderRadius: 2, cursor: 'pointer',
    transition: 'transform 120ms cubic-bezier(.7,0,.2,1.4), box-shadow 120ms',
    display: 'inline-flex', alignItems: 'center', gap: 8,
  };
  const palettes = {
    primary: { background: 'var(--rust)', color: 'var(--paper)' },
    ghost:   { background: 'transparent', color: 'var(--ink)' },
    amber:   { background: 'var(--amber)', color: 'var(--ink)' },
    ink:     { background: 'var(--ink)', color: 'var(--paper)' },
  };
  const shadow = press ? '0 0 0 0 var(--ink)' : hover ? '6px 6px 0 0 var(--ink)' : '4px 4px 0 0 var(--ink)';
  const transform = press ? 'translate(2px,2px)' : hover ? 'translate(-1px,-1px)' : 'translate(0,0)';
  return (
    <button
      onClick={onClick}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => { setHover(false); setPress(false); }}
      onMouseDown={() => setPress(true)}
      onMouseUp={() => setPress(false)}
      style={{ ...base, ...palettes[variant], boxShadow: shadow, transform, ...style }}
    >{children}</button>
  );
};

const Chip = ({ children, tone = 'paper', style = {} }) => {
  const palettes = {
    paper: { background: 'var(--paper)', color: 'var(--ink)' },
    amber: { background: 'var(--amber)', color: 'var(--ink)' },
    rust: { background: 'var(--rust)', color: 'var(--paper)' },
    ink: { background: 'var(--ink)', color: 'var(--paper)' },
  };
  return (
    <span style={{
      fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 11,
      textTransform: 'uppercase', letterSpacing: '0.14em',
      padding: '6px 12px', border: '2px solid var(--ink)',
      borderRadius: 999, display: 'inline-flex', alignItems: 'center', gap: 6,
      ...palettes[tone], ...style
    }}>{children}</span>
  );
};

const Stamp = ({ children, rotate = -4, color = 'var(--rust)' }) => (
  <span style={{
    fontFamily: 'var(--font-sans)', fontWeight: 700, fontSize: 13,
    letterSpacing: '0.18em', textTransform: 'uppercase',
    padding: '8px 14px', border: `2px solid ${color}`,
    outline: `2px solid ${color}`, outlineOffset: 3,
    color, transform: `rotate(${rotate}deg)`, display: 'inline-block',
  }}>{children}</span>
);

window.RRPrim = { Eyebrow, Button, Chip, Stamp };
