import { ReactNode, useState } from "react";
import { DropdownMenu, Popover, Switch as SwitchPrimitive, Tooltip } from "radix-ui";
import { ChevronDownIcon, EyeIcon, EyeOffIcon, MinusIcon, PlusIcon, XIcon, type LucideIcon } from "lucide-react";
import { translate } from "../../lib/i18n";

/**
 * The settings page's own parts: a group is a titled panel of rows, a row is
 * a name on the left and its control on the right. Everything on the page is
 * built from these so it reads as one instrument rather than eleven cards.
 *
 * Strings handed to these are translated here; a string that is already in
 * the reader's language passes through unchanged.
 */

export function Group({ icon: Icon, title, action, children, hidden, testId }: {
  icon?: LucideIcon;
  title: string;
  action?: ReactNode;
  children: ReactNode;
  hidden?: boolean;
  testId?: string;
}) {
  if (hidden) return null;
  return (
    <section className="st-group" data-testid={testId}>
      <header className="st-group-h">
        {Icon && <Icon size={15} strokeWidth={1.8} aria-hidden />}
        <h3>{translate(title)}</h3>
        {action && <div className="st-group-a">{action}</div>}
      </header>
      <div className="st-panel">{children}</div>
    </section>
  );
}

/** A name and its control. stack puts the control under the name, for lists
 *  and anything wider than the right-hand column; lead is an avatar or icon
 *  before the name. */
export function Row({ label, sub, children, stack, mono, lead, dim, testId, sub2 }: {
  label: ReactNode;
  sub?: ReactNode;
  sub2?: ReactNode;
  children?: ReactNode;
  stack?: boolean;
  mono?: boolean;
  lead?: ReactNode;
  dim?: boolean;
  testId?: string;
}) {
  return (
    <div className={`st-row${stack ? " stack" : ""}${lead ? " lead" : ""}${dim ? " dim" : ""}`} data-testid={testId}>
      {lead && !stack && <div className="st-row-lead">{lead}</div>}
      <div className="st-row-l">
        <div className={`st-row-t${mono ? " mono" : ""}`}>{typeof label === "string" ? translate(label) : label}</div>
        {sub && <div className="st-row-s">{sub}</div>}
        {sub2}
      </div>
      {children !== undefined && children !== null && children !== false && <div className="st-row-c">{children}</div>}
    </div>
  );
}

/** A row's worth of anything that is not a name and a control: a list, a
 *  form opened from a button, a QR code. */
export function Block({ children, testId }: { children: ReactNode; testId?: string }) {
  return <div className="st-row st-block" data-testid={testId}>{children}</div>;
}

/** A square letter or icon at the start of a row. */
export function Avatar({ children, tone }: { children: ReactNode; tone?: "accent" | "muted" }) {
  return <span className={`st-avatar${tone ? ` ${tone}` : ""}`} aria-hidden>{children}</span>;
}

export function Toggle({ on, onChange, label, disabled, testId }: {
  on: boolean;
  onChange: (on: boolean) => void;
  label?: string;
  disabled?: boolean;
  testId?: string;
}) {
  return (
    <SwitchPrimitive.Root className="st-switch" checked={on} onCheckedChange={onChange}
      aria-label={label ? translate(label) : undefined} disabled={disabled} data-testid={testId}>
      <SwitchPrimitive.Thumb className="st-switch-k" />
    </SwitchPrimitive.Root>
  );
}

export function Tip({ text, children }: { text: string; children: ReactNode }) {
  return (
    <Tooltip.Provider delayDuration={250}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content className="st-tip" sideOffset={6}>{text}</Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
  );
}

type IconBtnProps = {
  icon: LucideIcon;
  label: string;
  onClick?: () => void;
  tone?: "danger" | "accent";
  disabled?: boolean;
  spin?: boolean;
  pressed?: boolean;
};

export function IconBtn({ icon: Icon, label, onClick, tone, disabled, spin, pressed, ...rest }: IconBtnProps & Record<string, unknown>) {
  const text = translate(label);
  return (
    <Tip text={text}>
      <button type="button" className={`st-ib${tone ? ` ${tone}` : ""}${pressed ? " on" : ""}`} aria-label={text}
        aria-pressed={pressed} onClick={onClick} disabled={disabled} {...rest}>
        <Icon size={16} strokeWidth={1.8} className={spin ? "st-spin" : undefined} />
      </button>
    </Tip>
  );
}

/** An icon button that asks once before it acts: sign out, remove, unpair. */
export function ConfirmBtn({ icon, label, title, sub, ok, onConfirm, tone = "danger", disabled, testId }: {
  icon: LucideIcon;
  label: string;
  title: string;
  sub?: string;
  ok?: string;
  onConfirm: () => void | Promise<void>;
  tone?: "danger" | "accent";
  disabled?: boolean;
  testId?: string;
}) {
  const [open, setOpen] = useState(false);
  const Icon = icon;
  const text = translate(label);
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Tip text={text}>
        <Popover.Trigger asChild>
          <button type="button" className={`st-ib ${tone}${open ? " on" : ""}`} aria-label={text} disabled={disabled} data-testid={testId}>
            <Icon size={16} strokeWidth={1.8} />
          </button>
        </Popover.Trigger>
      </Tip>
      <Popover.Portal>
        <Popover.Content className="st-pop" side="top" align="end" sideOffset={8} collisionPadding={12} role="dialog" aria-label={translate(title)}>
          <div className="st-pop-t">{translate(title)}</div>
          {sub && <div className="st-pop-s">{sub}</div>}
          <div className="st-pop-b">
            <button type="button" className="st-btn" onClick={() => setOpen(false)}>{translate("Cancel")}</button>
            <button
              type="button"
              className={`st-btn ${tone === "danger" ? "danger" : "pri"}`}
              data-testid={testId ? `${testId}-ok` : undefined}
              onClick={async () => { setOpen(false); await onConfirm(); }}
            >
              {translate(ok ?? label)}
            </button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** A small menu from one trigger, for a list of choices that would otherwise
 *  be a row of buttons. */
export function Menu({ trigger, items, disabled }: {
  trigger: ReactNode;
  items: { key: string; label: ReactNode; onSelect: () => void; hint?: string; lead?: ReactNode; disabled?: boolean }[];
  disabled?: boolean;
}) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild disabled={disabled}>{trigger}</DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content className="st-menu" align="end" sideOffset={6} collisionPadding={12}>
          {items.map((it) => (
            <DropdownMenu.Item key={it.key} className="st-menu-i" onSelect={it.onSelect} title={it.hint} disabled={it.disabled}>
              {it.lead}
              <span className="st-menu-l">{it.label}</span>
            </DropdownMenu.Item>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

export function Btn({ children, onClick, pri, disabled, icon: Icon, trail: Trail, ...rest }: {
  children: ReactNode;
  onClick?: () => void;
  pri?: boolean;
  disabled?: boolean;
  icon?: LucideIcon;
  trail?: LucideIcon;
} & Record<string, unknown>) {
  return (
    <button type="button" className={`st-btn${pri ? " pri" : ""}`} onClick={onClick} disabled={disabled} {...rest}>
      {Icon && <Icon size={14} strokeWidth={1.9} />}
      {typeof children === "string" ? translate(children) : children}
      {Trail && <Trail size={13} strokeWidth={1.9} />}
    </button>
  );
}

export function Text({ value, onChange, placeholder, mono, type = "text", width, label, onEnter, disabled, inputMode, align, testId, readOnly }: {
  value: string | number;
  onChange?: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  type?: string;
  width?: number;
  label?: string;
  onEnter?: () => void;
  disabled?: boolean;
  inputMode?: "numeric" | "text" | "url";
  align?: "right";
  testId?: string;
  readOnly?: boolean;
}) {
  return (
    <input
      className={`st-in${mono ? " mono" : ""}${width ? " fixed" : ""}`}
      type={type}
      value={value}
      placeholder={placeholder ? translate(placeholder) : undefined}
      aria-label={label ? translate(label) : undefined}
      onChange={(e) => onChange?.(e.target.value)}
      onKeyDown={onEnter ? (e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing) { e.preventDefault(); onEnter(); } } : undefined}
      autoComplete="off"
      spellCheck={false}
      disabled={disabled}
      readOnly={readOnly}
      inputMode={inputMode}
      data-testid={testId}
      style={{ ...(width ? { width } : {}), ...(align ? { textAlign: align } : {}) }}
    />
  );
}

/** A whole number; empty reads as zero, which the settings take as "no limit". */
export function Num({ value, onChange, placeholder, label, min = 0, testId }: {
  value: number | undefined;
  onChange: (v: number) => void;
  placeholder?: string;
  label?: string;
  min?: number;
  testId?: string;
}) {
  return (
    <Text mono width={120} align="right" inputMode="numeric" label={label} placeholder={placeholder} testId={testId}
      value={value ? String(value) : ""}
      onChange={(v) => {
        const n = Number(v.replace(/\D/g, ""));
        onChange(Number.isFinite(n) ? Math.max(min, n) : min);
      }} />
  );
}

export function Secret({ value, onChange, placeholder, label, testId }: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  label?: string;
  testId?: string;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="st-secret">
      <input
        className="st-in mono"
        type={show ? "text" : "password"}
        value={value}
        placeholder={translate(placeholder ?? "Not set")}
        aria-label={label ? translate(label) : undefined}
        onChange={(e) => onChange(e.target.value)}
        autoComplete="off"
        spellCheck={false}
        data-testid={testId}
      />
      <IconBtn icon={show ? EyeOffIcon : EyeIcon} label={show ? "Hide" : "Show"} onClick={() => setShow((s) => !s)} />
    </div>
  );
}

export function Seg<T extends string>({ value, options, onChange, label, disabled, testId }: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  label?: string;
  disabled?: boolean;
  testId?: string;
}) {
  return (
    <div className="st-seg" role="radiogroup" aria-label={label ? translate(label) : undefined} data-testid={testId}>
      {options.map((o) => (
        <button
          key={o.value || "_"}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          className={value === o.value ? "on" : ""}
          disabled={disabled}
          onClick={() => onChange(o.value)}
        >
          {translate(o.label)}
        </button>
      ))}
    </div>
  );
}

/** A native select dressed as the inputs: the platform's own list, which is
 *  the right one on a phone. */
export function Select({ value, options, onChange, label, mono, testId, placeholder }: {
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  label?: string;
  mono?: boolean;
  testId?: string;
  placeholder?: string;
}) {
  const known = options.some((o) => o.value === value);
  return (
    <label className={`st-select${mono ? " mono" : ""}`}>
      <select value={value} onChange={(e) => onChange(e.target.value)} aria-label={label ? translate(label) : undefined} data-testid={testId}>
        {!known && <option value={value}>{value || translate(placeholder ?? "Choose")}</option>}
        {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
      <ChevronDownIcon size={14} strokeWidth={2} aria-hidden />
    </label>
  );
}

/** Removable words, and a plus that opens a field for one more. */
export function Chips({ items, onRemove, onAdd, removeLabel, addLabel, placeholder, mono, disabled, max, testId }: {
  items: string[];
  onRemove: (item: string) => void;
  onAdd?: (item: string) => void;
  removeLabel: string;
  addLabel?: string;
  placeholder?: string;
  mono?: boolean;
  disabled?: boolean;
  /** Shown before the rest fold into "+N". */
  max?: number;
  testId?: string;
}) {
  const [adding, setAdding] = useState(false);
  const [text, setText] = useState("");
  const [all, setAll] = useState(false);
  const shown = max && !all ? items.slice(0, max) : items;
  const hidden = items.length - shown.length;
  const add = () => {
    const v = text.trim();
    setText("");
    setAdding(false);
    if (v && onAdd) onAdd(v);
  };
  return (
    <div className="st-chips" data-testid={testId}>
      {shown.map((it) => (
        <span key={it} className={`st-chip${mono ? " mono" : ""}`}>
          {it}
          <button type="button" aria-label={`${translate(removeLabel)} ${it}`} title={translate(removeLabel)} disabled={disabled} onClick={() => onRemove(it)}>
            <XIcon size={12} strokeWidth={2} />
          </button>
        </span>
      ))}
      {hidden > 0 && <button type="button" className="st-chip-more" onClick={() => setAll(true)}>+{hidden}</button>}
      {onAdd && (adding ? (
        <input className="st-in st-chip-in" autoFocus value={text} placeholder={placeholder ? translate(placeholder) : undefined}
          aria-label={addLabel ? translate(addLabel) : undefined} disabled={disabled}
          onChange={(e) => setText(e.target.value)} onBlur={add}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing) { e.preventDefault(); add(); }
            if (e.key === "Escape") { setText(""); setAdding(false); }
          }} />
      ) : (
        <IconBtn icon={PlusIcon} label={addLabel ?? "Add"} disabled={disabled} onClick={() => setAdding(true)} />
      ))}
    </div>
  );
}

/** A small count with a minus and a plus. */
export function Stepper({ value, onChange, min, max, label, disabled }: {
  value: number;
  onChange: (v: number) => void;
  min: number;
  max: number;
  label: string;
  disabled?: boolean;
}) {
  const text = translate(label);
  return (
    <div className="st-step" role="group" aria-label={text}>
      <button type="button" aria-label={`${text} −`} disabled={disabled || value <= min} onClick={() => onChange(value - 1)}><MinusIcon size={13} strokeWidth={2} /></button>
      <span>{value}</span>
      <button type="button" aria-label={`${text} +`} disabled={disabled || value >= max} onClick={() => onChange(value + 1)}><PlusIcon size={13} strokeWidth={2} /></button>
    </div>
  );
}

/** A coloured dot and a word: ready, paused, failed. */
export function State({ tone, children, title }: { tone: "ok" | "off" | "bad" | "busy"; children: ReactNode; title?: string }) {
  return <span className={`st-state ${tone}`} title={title}><i />{typeof children === "string" ? translate(children) : children}</span>;
}

/** A line of small text under a control: a result, an error. */
export function Note({ children, bad, testId }: { children: ReactNode; bad?: boolean; testId?: string }) {
  return <div className={`st-note${bad ? " bad" : ""}`} data-testid={testId}>{children}</div>;
}
