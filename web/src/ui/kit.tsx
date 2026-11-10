import { useEffect, useId, useRef, useState, type ReactNode } from "react";

export function Plate({ small = false }: { small?: boolean }) {
  return (
    <span className="inline-flex rounded-[8px] bg-sign p-[3px] shadow-[0_1px_0_rgba(0,0,0,0.45)]">
      <span
        className={`rounded-[5px] border-2 border-white font-extrabold leading-none text-white ${
          small ? "px-2 pt-[3px] pb-[1px] text-[14px]" : "px-2.5 pt-[4px] pb-[2px] text-[18px]"
        }`}
      >
        Crosstown
      </span>
    </span>
  );
}

export function Phone({ children, label, framed }: { children: ReactNode; label: string; framed: boolean }) {
  if (!framed) {
    return (
      <section aria-label={label} className="relative h-full w-full overflow-hidden bg-paper text-ash">
        {children}
      </section>
    );
  }
  return (
    <section
      aria-label={label}
      className="relative h-full w-full rounded-[46px] bg-[#1c2833] p-[9px] shadow-[0_40px_80px_-30px_rgba(0,0,0,0.85),inset_0_0_0_1px_rgba(255,255,255,0.09)]"
    >
      <div className="relative h-full w-full overflow-hidden rounded-[37px] bg-paper text-ash">{children}</div>
    </section>
  );
}

type ButtonKind = "primary" | "quiet" | "danger" | "dark";

const kinds: Record<ButtonKind, string> = {
  primary: "bg-sign text-white hover:bg-sign-hi disabled:bg-slate/40",
  quiet: "bg-fog text-ash hover:bg-[#e2e8ea] disabled:text-slate",
  danger: "bg-fog text-danger hover:bg-[#f6e3e6]",
  dark: "bg-ash text-white hover:bg-[#1d3343] disabled:bg-slate/40",
};

export function Button({
  kind = "primary",
  children,
  onClick,
  disabled,
  wide = true,
}: {
  kind?: ButtonKind;
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  wide?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={`${kinds[kind]} ${wide ? "w-full" : ""} rounded-[14px] px-4 pt-[13px] pb-[11px] text-[16px] font-bold transition-colors disabled:cursor-not-allowed`}
    >
      {children}
    </button>
  );
}

export function Stars({ value, onRate }: { value: number; onRate: (n: number) => void }) {
  const [hover, setHover] = useState(0);
  const shown = hover || value;
  return (
    <div className="flex gap-1.5" role="radiogroup" aria-label="Rate your driver" onPointerLeave={() => setHover(0)}>
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          role="radio"
          aria-checked={value === n}
          aria-label={`${n} star${n > 1 ? "s" : ""}`}
          onPointerEnter={() => setHover(n)}
          onClick={() => onRate(n)}
          className="rounded-md p-0.5"
        >
          <svg width="34" height="34" viewBox="0 0 24 24" aria-hidden="true">
            <path
              d="M12 2.8l2.75 5.6 6.15.9-4.45 4.35 1.05 6.1L12 16.85 6.5 19.75l1.05-6.1L3.1 9.3l6.15-.9z"
              fill={n <= shown ? "#c7821c" : "#dfe5e8"}
            />
          </svg>
        </button>
      ))}
    </div>
  );
}

export function Spark({
  title,
  values,
  reference,
  referenceLabel,
  color,
  format,
  times,
}: {
  title: string;
  values: number[];
  reference?: number;
  referenceLabel?: string;
  color: string;
  format: (v: number) => string;
  times: string[];
}) {
  const id = useId();
  const [hover, setHover] = useState<number | null>(null);
  const w = 220;
  const h = 46;
  const pad = 3;
  const max = Math.max(1, reference ?? 0, ...values) * 1.1;
  const x = (i: number) => (values.length < 2 ? 0 : (i / (values.length - 1)) * w);
  const y = (v: number) => h - pad - (v / max) * (h - 2 * pad);
  const line = values.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const shown = hover ?? values.length - 1;
  return (
    <figure className="min-w-0 flex-1">
      <figcaption className="flex items-baseline justify-between gap-2 text-[12px] text-mute">
        <span id={id}>{title}</span>
        <span className="text-[13px] font-semibold text-ink">
          {values.length ? format(values[shown]) : "-"}
          {hover !== null && <span className="ml-1 font-normal text-mute">at {times[hover]}</span>}
        </span>
      </figcaption>
      <svg
        viewBox={`0 0 ${w} ${h}`}
        className="mt-1 block h-[46px] w-full overflow-visible"
        role="img"
        aria-labelledby={id}
        onPointerMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          const i = Math.round(((e.clientX - r.left) / r.width) * (values.length - 1));
          setHover(Math.max(0, Math.min(values.length - 1, i)));
        }}
        onPointerLeave={() => setHover(null)}
      >
        <line x1="0" x2={w} y1={h - pad} y2={h - pad} stroke="#263d50" strokeWidth="1" vectorEffect="non-scaling-stroke" />
        {reference !== undefined && (
          <line
            x1="0"
            x2={w}
            y1={y(reference)}
            y2={y(reference)}
            stroke="#8ea3b3"
            strokeWidth="1.5"
            strokeDasharray="4 4"
            vectorEffect="non-scaling-stroke"
          />
        )}
        <path d={line} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
        {values.length > 0 && (
          <>
            <line x1={x(shown)} x2={x(shown)} y1="0" y2={h} stroke="#8ea3b3" strokeOpacity={hover === null ? 0 : 0.5} vectorEffect="non-scaling-stroke" />
            <circle cx={x(shown)} cy={y(values[shown])} r="3.5" fill={color} stroke="#152330" strokeWidth="2" />
          </>
        )}
      </svg>
      {reference !== undefined && referenceLabel && (
        <div className="mt-1 flex items-center gap-3 text-[11px] text-mute">
          <span className="flex items-center gap-1">
            <span className="inline-block h-[2px] w-3" style={{ background: color }} />
            Crosstown
          </span>
          <span className="flex items-center gap-1">
            <span className="inline-block w-3 border-t-[1.5px] border-dashed border-mute" />
            {referenceLabel}
          </span>
        </div>
      )}
    </figure>
  );
}

export function Toast({ text, onClose }: { text: string | null; onClose: () => void }) {
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    if (!text) return;
    const t = window.setTimeout(() => close.current(), 6000);
    return () => window.clearTimeout(t);
  }, [text]);
  if (!text) return null;
  return (
    <button
      type="button"
      role="status"
      onClick={onClose}
      className="rise absolute inset-x-3 top-3 z-20 rounded-[14px] bg-ash px-4 pt-[13px] pb-[11px] text-left text-[14px] font-semibold text-white shadow-lg"
    >
      {text}
    </button>
  );
}
