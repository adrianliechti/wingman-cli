// oxlint-disable react/only-export-components -- provider hooks and dialog styles intentionally share this UI module.
import {
	createContext,
	type ReactNode,
	useCallback,
	useContext,
	useEffect,
	useEffectEvent,
	useId,
	useRef,
	useState,
} from "react";
import { createPortal } from "react-dom";
import { AlertCircle, CheckCircle2, Info, X } from "lucide-react";

type ToastTone = "error" | "info" | "success";

interface ToastInput {
	title: string;
	description?: string;
	tone?: ToastTone;
}

interface ToastItem extends ToastInput {
	id: number;
}

const ToastContext = createContext<((toast: ToastInput) => void) | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
	const [toasts, setToasts] = useState<ToastItem[]>([]);
	const nextId = useRef(0);
	const showToast = useCallback((toast: ToastInput) => {
		const id = ++nextId.current;
		setToasts((items) => [...items.slice(-3), { ...toast, id }]);
		window.setTimeout(
			() => setToasts((items) => items.filter((item) => item.id !== id)),
			toast.tone === "error" ? 7000 : 4000,
		);
	}, []);

	return (
		<ToastContext.Provider value={showToast}>
			{children}
			{createPortal(
				<div
					className="pointer-events-none fixed bottom-4 right-4 z-200 flex w-[360px] max-w-[calc(100vw-2rem)] flex-col gap-2"
					aria-live="polite"
					aria-atomic="false"
				>
					{toasts.map((toast) => (
						<Toast
							key={toast.id}
							toast={toast}
							onClose={() =>
								setToasts((items) =>
									items.filter((item) => item.id !== toast.id),
								)
							}
						/>
					))}
				</div>,
				document.body,
			)}
		</ToastContext.Provider>
	);
}

export function useToast() {
	const value = useContext(ToastContext);
	if (!value) throw new Error("useToast must be used inside ToastProvider");
	return value;
}

function Toast({ toast, onClose }: { toast: ToastItem; onClose: () => void }) {
	const Icon =
		toast.tone === "error"
			? AlertCircle
			: toast.tone === "success"
				? CheckCircle2
				: Info;
	return (
		<div
			className={`pointer-events-auto flex items-start gap-2.5 rounded-lg border bg-bg-elevated/95 px-3 py-2.5 shadow-2xl backdrop-blur-sm ${
				toast.tone === "error" ? "border-danger/40" : "border-border"
			}`}
			role={toast.tone === "error" ? "alert" : "status"}
		>
			<Icon
				size={15}
				className={`mt-0.5 shrink-0 ${
					toast.tone === "error"
						? "text-danger"
						: toast.tone === "success"
							? "text-success"
							: "text-fg-muted"
				}`}
			/>
			<div className="min-w-0 flex-1">
				<div className="text-[12px] font-medium text-fg">{toast.title}</div>
				{toast.description && (
					<div className="mt-0.5 break-words text-[11px] text-fg-muted">
						{toast.description}
					</div>
				)}
			</div>
			<button
				type="button"
				onClick={onClose}
				className="-mr-1 flex h-6 w-6 shrink-0 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
				aria-label="Dismiss notification"
			>
				<X size={12} />
			</button>
		</div>
	);
}

interface DialogProps {
	open: boolean;
	title: string;
	description?: ReactNode;
	onClose: () => void;
	children: ReactNode;
	initialFocus?: "first" | "last";
}

export function Dialog({
	open,
	title,
	description,
	onClose,
	children,
	initialFocus = "last",
}: DialogProps) {
	const titleId = useId();
	const descriptionId = useId();
	const panelRef = useRef<HTMLDivElement>(null);
	const returnFocusRef = useRef<HTMLElement | null>(null);
	const handleClose = useEffectEvent(onClose);

	useEffect(() => {
		if (!open) return;
		returnFocusRef.current = document.activeElement as HTMLElement | null;
		const frame = requestAnimationFrame(() => {
			const focusable = getFocusable(panelRef.current);
			const target =
				initialFocus === "first"
					? focusable[0]
					: focusable[focusable.length - 1];
			target?.focus();
		});
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				event.preventDefault();
				handleClose();
				return;
			}
			if (event.key !== "Tab") return;
			const focusable = getFocusable(panelRef.current);
			if (focusable.length === 0) return;
			const first = focusable[0];
			const last = focusable[focusable.length - 1];
			if (event.shiftKey && document.activeElement === first) {
				event.preventDefault();
				last.focus();
			} else if (!event.shiftKey && document.activeElement === last) {
				event.preventDefault();
				first.focus();
			}
		};
		document.addEventListener("keydown", onKeyDown);
		return () => {
			cancelAnimationFrame(frame);
			document.removeEventListener("keydown", onKeyDown);
			returnFocusRef.current?.focus();
		};
	}, [initialFocus, open]);

	if (!open) return null;
	return createPortal(
		<div
			className="fixed inset-0 z-150 flex items-center justify-center bg-bg/65 p-4"
			onMouseDown={(event) => {
				if (event.target === event.currentTarget) onClose();
			}}
		>
			<div
				ref={panelRef}
				role="dialog"
				aria-modal="true"
				aria-labelledby={titleId}
				aria-describedby={description ? descriptionId : undefined}
				className="w-full max-w-md rounded-xl border border-border bg-bg-elevated p-4 shadow-2xl"
			>
				<h2 id={titleId} className="text-[13px] font-semibold text-fg">
					{title}
				</h2>
				{description && (
					<div
						id={descriptionId}
						className="mt-1.5 text-[12px] leading-relaxed text-fg-muted"
					>
						{description}
					</div>
				)}
				<div className="mt-4 flex flex-wrap justify-end gap-2">{children}</div>
			</div>
		</div>,
		document.body,
	);
}

export const dialogButtonClass =
	"inline-flex h-8 items-center justify-center gap-1.5 whitespace-nowrap rounded-md border border-border bg-bg-surface px-3 text-[12px] font-medium text-fg-muted transition-colors hover:border-border-strong hover:bg-bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus";

export const dialogPrimaryButtonClass = `${dialogButtonClass} border-button-primary-border bg-button-primary text-button-primary-fg shadow-sm hover:border-button-primary-border hover:bg-button-primary-hover hover:text-button-primary-fg`;

function getFocusable(root: HTMLElement | null): HTMLElement[] {
	if (!root) return [];
	return Array.from(
		root.querySelectorAll<HTMLElement>(
			'button:not([disabled]), [href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
		),
	);
}
