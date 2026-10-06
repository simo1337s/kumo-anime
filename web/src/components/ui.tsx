import * as DialogPrimitive from "@radix-ui/react-dialog"
import * as DropdownPrimitive from "@radix-ui/react-dropdown-menu"
import * as PopoverPrimitive from "@radix-ui/react-popover"
import * as SwitchPrimitive from "@radix-ui/react-switch"
import * as TooltipPrimitive from "@radix-ui/react-tooltip"
import { ChevronDown, Loader2, X } from "lucide-react"
import React, { forwardRef } from "react"
import { cn } from "@/lib/utils"

// ---------------------------------------------------------------------------
// Buttons

type ButtonVariant = "primary" | "white" | "subtle" | "ghost" | "outline" | "danger" | "success"
type ButtonSize = "xs" | "sm" | "md" | "lg"

const variants: Record<ButtonVariant, string> = {
    primary: "bg-brand text-white hover:brightness-110 shadow-[0_6px_24px_-8px_var(--brand)]",
    white: "bg-white text-black hover:bg-white/90",
    subtle: "bg-white/[0.06] text-fg hover:bg-white/[0.1] border border-line",
    ghost: "text-muted hover:text-fg hover:bg-white/[0.06]",
    outline: "border border-line-strong text-fg hover:bg-white/[0.05]",
    danger: "bg-rose-500/15 text-rose-300 hover:bg-rose-500/25 border border-rose-500/20",
    success: "bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25 border border-emerald-500/20",
}
const sizes: Record<ButtonSize, string> = {
    xs: "h-7 px-2.5 text-xs gap-1.5 rounded-lg",
    sm: "h-8 px-3 text-sm gap-1.5 rounded-lg",
    md: "h-10 px-4 text-sm gap-2 rounded-xl",
    lg: "h-12 px-6 text-base gap-2.5 rounded-xl",
}

export type ButtonProps = React.ButtonHTMLAttributes<HTMLButtonElement> & {
    variant?: ButtonVariant
    size?: ButtonSize
    loading?: boolean
    icon?: React.ReactNode
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
    { variant = "subtle", size = "md", loading, icon, className, children, disabled, ...props },
    ref,
) {
    return (
        <button
            ref={ref}
            disabled={disabled || loading}
            className={cn(
                "focus-ring inline-flex shrink-0 items-center justify-center font-medium whitespace-nowrap transition-all duration-150 select-none active:scale-[0.98] disabled:opacity-50 disabled:active:scale-100",
                variants[variant],
                sizes[size],
                className,
            )}
            {...props}
        >
            {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
            {children}
        </button>
    )
})

export const IconButton = forwardRef<HTMLButtonElement, ButtonProps & { label?: string }>(function IconButton(
    { variant = "ghost", size = "md", className, label, children, ...props },
    ref,
) {
    const dims = { xs: "size-7", sm: "size-8", md: "size-10", lg: "size-12" }[size]
    const btn = (
        <Button ref={ref} variant={variant} size={size} className={cn(dims, "px-0", className)} aria-label={label} {...props}>
            {children}
        </Button>
    )
    return label ? <Tooltip content={label}>{btn}</Tooltip> : btn
})

// ---------------------------------------------------------------------------
// Form controls

export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement> & { icon?: React.ReactNode }>(
    function Input({ className, icon, ...props }, ref) {
        return (
            <div className="relative w-full">
                {icon && <span className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-subtle">{icon}</span>}
                <input
                    ref={ref}
                    className={cn(
                        "h-10 w-full rounded-xl border border-line bg-surface-2 px-3 text-sm text-fg placeholder:text-subtle",
                        "transition-colors outline-none focus:border-brand/60 focus:bg-surface-3 disabled:opacity-60",
                        icon && "pl-9",
                        className,
                    )}
                    {...props}
                />
            </div>
        )
    },
)

export const Textarea = forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea(
    { className, ...props },
    ref,
) {
    return (
        <textarea
            ref={ref}
            className={cn(
                "min-h-24 w-full rounded-xl border border-line bg-surface-2 px-3 py-2 text-sm text-fg placeholder:text-subtle outline-none focus:border-brand/60",
                className,
            )}
            {...props}
        />
    )
})

export function Select({
    value,
    onChange,
    options,
    className,
    disabled,
}: {
    value: string
    onChange: (v: string) => void
    options: { value: string; label: string }[]
    className?: string
    disabled?: boolean
}) {
    return (
        <div className={cn("relative", className)}>
            <select
                value={value}
                disabled={disabled}
                onChange={e => onChange(e.target.value)}
                className="h-10 w-full cursor-pointer appearance-none rounded-xl border border-line bg-surface-2 pr-9 pl-3 text-sm text-fg outline-none focus:border-brand/60 disabled:opacity-60"
            >
                {options.map(o => (
                    <option key={o.value} value={o.value} className="bg-surface-2">
                        {o.label}
                    </option>
                ))}
            </select>
            <ChevronDown className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-subtle" />
        </div>
    )
}

export function Switch({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
    return (
        <SwitchPrimitive.Root
            checked={checked}
            onCheckedChange={onChange}
            disabled={disabled}
            className="focus-ring relative h-6 w-11 shrink-0 rounded-full bg-white/[0.12] transition-colors data-[state=checked]:bg-brand disabled:opacity-50"
        >
            <SwitchPrimitive.Thumb className="block size-[18px] translate-x-[3px] rounded-full bg-white shadow transition-transform duration-200 data-[state=checked]:translate-x-[23px]" />
        </SwitchPrimitive.Root>
    )
}

export function Field({ label, help, children, className }: { label?: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; className?: string }) {
    return (
        <label className={cn("flex flex-col gap-1.5", className)}>
            {label && <span className="text-sm font-medium text-fg/90">{label}</span>}
            {children}
            {help && <span className="text-xs text-subtle">{help}</span>}
        </label>
    )
}

// ---------------------------------------------------------------------------
// Display

export function Badge({ children, className, tone = "gray" }: { children: React.ReactNode; className?: string; tone?: "gray" | "brand" | "green" | "amber" | "red" | "blue" }) {
    const tones = {
        gray: "bg-white/[0.07] text-fg/80 border-white/[0.06]",
        brand: "bg-brand-soft text-brand-strong border-brand/25",
        green: "bg-emerald-500/12 text-emerald-300 border-emerald-500/20",
        amber: "bg-amber-500/12 text-amber-300 border-amber-500/20",
        red: "bg-rose-500/12 text-rose-300 border-rose-500/20",
        blue: "bg-sky-500/12 text-sky-300 border-sky-500/20",
    }
    return (
        <span className={cn("inline-flex h-6 items-center gap-1 rounded-md border px-2 text-[11px] font-semibold tracking-wide whitespace-nowrap", tones[tone], className)}>
            {children}
        </span>
    )
}

export function Skeleton({ className }: { className?: string }) {
    return <div className={cn("shimmer rounded-xl", className)} />
}

export function Spinner({ className }: { className?: string }) {
    return <Loader2 className={cn("size-5 animate-spin text-brand", className)} />
}

export function Progress({ value, className, barClassName }: { value: number; className?: string; barClassName?: string }) {
    return (
        <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-white/[0.08]", className)}>
            <div className={cn("h-full rounded-full bg-brand transition-[width] duration-500", barClassName)} style={{ width: `${Math.max(0, Math.min(100, value * 100))}%` }} />
        </div>
    )
}

export function EmptyState({ icon, title, children, action }: { icon?: React.ReactNode; title: string; children?: React.ReactNode; action?: React.ReactNode }) {
    return (
        <div className="flex flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-line px-6 py-16 text-center fade-in">
            {icon && <div className="grid size-14 place-items-center rounded-2xl bg-white/[0.04] text-subtle">{icon}</div>}
            <h3 className="text-lg font-semibold">{title}</h3>
            {children && <div className="max-w-md text-sm text-muted">{children}</div>}
            {action}
        </div>
    )
}

export function SectionHeader({ title, subtitle, action, className }: { title: React.ReactNode; subtitle?: React.ReactNode; action?: React.ReactNode; className?: string }) {
    return (
        <div className={cn("mb-4 flex items-end justify-between gap-4", className)}>
            <div>
                <h2 className="text-2xl font-bold tracking-tight md:text-[1.7rem]">{title}</h2>
                {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
            </div>
            {action}
        </div>
    )
}

// ---------------------------------------------------------------------------
// Tabs

export function Tabs<T extends string>({
    value,
    onChange,
    tabs,
    className,
}: {
    value: T
    onChange: (v: T) => void
    tabs: { value: T; label: React.ReactNode; icon?: React.ReactNode; count?: number }[]
    className?: string
}) {
    return (
        <div className={cn("no-scrollbar inline-flex max-w-full gap-1 overflow-x-auto rounded-xl border border-line bg-surface-1 p-1", className)}>
            {tabs.map(t => (
                <button
                    key={t.value}
                    onClick={() => onChange(t.value)}
                    className={cn(
                        "focus-ring flex h-8 items-center gap-2 rounded-lg px-3.5 text-sm font-medium whitespace-nowrap transition-all",
                        value === t.value ? "bg-white/[0.09] text-fg shadow-sm" : "text-muted hover:text-fg",
                    )}
                >
                    {t.icon}
                    {t.label}
                    {t.count !== undefined && <span className="rounded-md bg-white/[0.08] px-1.5 text-[11px] text-muted">{t.count}</span>}
                </button>
            ))}
        </div>
    )
}

// ---------------------------------------------------------------------------
// Overlays

export function Tooltip({ content, children, side = "top" }: { content: React.ReactNode; children: React.ReactNode; side?: "top" | "right" | "bottom" | "left" }) {
    return (
        <TooltipPrimitive.Root delayDuration={250}>
            <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
            <TooltipPrimitive.Portal>
                <TooltipPrimitive.Content
                    side={side}
                    sideOffset={8}
                    className="z-[100] rounded-lg border border-line-strong bg-surface-4 px-2.5 py-1.5 text-xs font-medium text-fg shadow-xl fade-in"
                >
                    {content}
                </TooltipPrimitive.Content>
            </TooltipPrimitive.Portal>
        </TooltipPrimitive.Root>
    )
}

export const TooltipProvider = TooltipPrimitive.Provider

export function Dialog({
    open,
    onOpenChange,
    title,
    description,
    children,
    className,
    footer,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    title?: React.ReactNode
    description?: React.ReactNode
    children?: React.ReactNode
    className?: string
    footer?: React.ReactNode
}) {
    return (
        <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
            <DialogPrimitive.Portal>
                <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm fade-in" />
                <DialogPrimitive.Content
                    className={cn(
                        "fixed top-1/2 left-1/2 z-50 flex max-h-[88vh] w-[min(92vw,560px)] -translate-x-1/2 -translate-y-1/2 flex-col rounded-2xl border border-line-strong bg-surface-1 shadow-2xl rise-in outline-none",
                        className,
                    )}
                    aria-describedby={undefined}
                >
                    {(title || description) && (
                        <div className="flex items-start justify-between gap-4 border-b border-line px-6 py-4">
                            <div>
                                {title && <DialogPrimitive.Title className="text-lg font-semibold">{title}</DialogPrimitive.Title>}
                                {description && <DialogPrimitive.Description className="mt-0.5 text-sm text-muted">{description}</DialogPrimitive.Description>}
                            </div>
                            <DialogPrimitive.Close className="focus-ring -mr-2 grid size-8 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-fg">
                                <X className="size-4" />
                            </DialogPrimitive.Close>
                        </div>
                    )}
                    <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">{children}</div>
                    {footer && <div className="flex justify-end gap-2 border-t border-line px-6 py-4">{footer}</div>}
                </DialogPrimitive.Content>
            </DialogPrimitive.Portal>
        </DialogPrimitive.Root>
    )
}

export const Dropdown = DropdownPrimitive.Root
export const DropdownTrigger = DropdownPrimitive.Trigger

export function DropdownContent({ children, align = "end", className }: { children: React.ReactNode; align?: "start" | "center" | "end"; className?: string }) {
    return (
        <DropdownPrimitive.Portal>
            <DropdownPrimitive.Content
                align={align}
                sideOffset={8}
                className={cn("z-[60] min-w-52 rounded-xl border border-line-strong bg-surface-2 p-1.5 shadow-2xl fade-in", className)}
            >
                {children}
            </DropdownPrimitive.Content>
        </DropdownPrimitive.Portal>
    )
}

export function DropdownItem({ children, onSelect, icon, danger, disabled }: { children: React.ReactNode; onSelect?: () => void; icon?: React.ReactNode; danger?: boolean; disabled?: boolean }) {
    return (
        <DropdownPrimitive.Item
            onSelect={onSelect}
            disabled={disabled}
            className={cn(
                "flex h-9 cursor-pointer items-center gap-2.5 rounded-lg px-2.5 text-sm outline-none select-none data-[disabled]:opacity-40 data-[highlighted]:bg-white/[0.07]",
                danger ? "text-rose-300" : "text-fg",
            )}
        >
            {icon && <span className="text-muted [&>svg]:size-4">{icon}</span>}
            {children}
        </DropdownPrimitive.Item>
    )
}

export function DropdownSeparator() {
    return <DropdownPrimitive.Separator className="my-1 h-px bg-line" />
}

export function DropdownLabel({ children }: { children: React.ReactNode }) {
    return <DropdownPrimitive.Label className="px-2.5 py-1.5 text-xs font-semibold tracking-wider text-subtle uppercase">{children}</DropdownPrimitive.Label>
}

export function Popover({
    trigger,
    children,
    open,
    onOpenChange,
    side = "right",
    align = "end",
    className,
}: {
    trigger: React.ReactNode
    children: React.ReactNode
    open?: boolean
    onOpenChange?: (v: boolean) => void
    side?: "top" | "right" | "bottom" | "left"
    align?: "start" | "center" | "end"
    className?: string
}) {
    return (
        <PopoverPrimitive.Root open={open} onOpenChange={onOpenChange}>
            <PopoverPrimitive.Trigger asChild>{trigger}</PopoverPrimitive.Trigger>
            <PopoverPrimitive.Portal>
                <PopoverPrimitive.Content
                    side={side}
                    align={align}
                    sideOffset={12}
                    collisionPadding={12}
                    className={cn("z-[60] max-h-[85vh] overflow-y-auto rounded-2xl border border-line-strong bg-surface-1 p-4 shadow-2xl outline-none fade-in", className)}
                >
                    {children}
                </PopoverPrimitive.Content>
            </PopoverPrimitive.Portal>
        </PopoverPrimitive.Root>
    )
}
