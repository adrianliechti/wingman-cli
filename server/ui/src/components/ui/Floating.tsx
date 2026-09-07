import {
	autoUpdate,
	flip,
	FloatingFocusManager,
	FloatingPortal,
	offset,
	shift,
	size,
	type Placement,
	type ReferenceType,
	useDismiss,
	useFloating,
	useInteractions,
	useRole,
} from "@floating-ui/react";
import {
	type KeyboardEvent,
	type ReactNode,
	useLayoutEffect,
	useMemo,
} from "react";

interface FloatingPoint {
	x: number;
	y: number;
}

type FloatingRole = "dialog" | "menu" | "listbox";

interface FloatingSurfaceProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	reference: Element | FloatingPoint | null;
	children: ReactNode;
	className?: string;
	placement?: Placement;
	gap?: number;
	role?: FloatingRole;
	label?: string;
	focusOnOpen?: boolean;
	returnFocus?: boolean;
	menuNavigation?: boolean;
	maxHeight?: number;
}

function isPoint(
	reference: Element | FloatingPoint,
): reference is FloatingPoint {
	return !(reference instanceof Element);
}

function moveMenuFocus(container: HTMLElement, event: KeyboardEvent) {
	const items = Array.from(
		container.querySelectorAll<HTMLElement>(
			'[role="menuitem"]:not([disabled]), [role="menuitemradio"]:not([disabled]), [role="menuitemcheckbox"]:not([disabled])',
		),
	).filter((item) => item.getAttribute("aria-disabled") !== "true");
	if (items.length === 0) return;

	const current = items.indexOf(document.activeElement as HTMLElement);
	let next: number | undefined;
	if (event.key === "ArrowDown")
		next = current < 0 ? 0 : (current + 1) % items.length;
	if (event.key === "ArrowUp")
		next =
			current < 0
				? items.length - 1
				: (current - 1 + items.length) % items.length;
	if (event.key === "Home") next = 0;
	if (event.key === "End") next = items.length - 1;
	if (next === undefined) return;

	event.preventDefault();
	items[next]?.focus();
}

/**
 * Shared portal and positioning boundary for menus, pickers, and popovers.
 * Visual styling remains with the caller; this component owns geometry,
 * dismissal, and focus behavior.
 */
export function FloatingSurface({
	open,
	onOpenChange,
	reference,
	children,
	className,
	placement = "bottom-start",
	gap = 4,
	role = "dialog",
	label,
	focusOnOpen = false,
	returnFocus = true,
	menuNavigation = false,
	maxHeight,
}: FloatingSurfaceProps) {
	const contextMenu =
		role === "menu" &&
		maxHeight === undefined &&
		reference !== null &&
		isPoint(reference);
	const boundaryPadding = contextMenu ? 4 : 8;
	const virtualReference = useMemo<ReferenceType | null>(() => {
		if (!reference || !isPoint(reference)) return null;
		return {
			getBoundingClientRect: () => new DOMRect(reference.x, reference.y),
		};
	}, [reference]);

	const {
		refs: { setReference, setFloating },
		floatingStyles,
		context,
	} = useFloating({
		open,
		onOpenChange,
		placement,
		strategy: "fixed",
		whileElementsMounted: autoUpdate,
		middleware: [
			offset(gap),
			flip({ padding: boundaryPadding }),
			shift({ padding: boundaryPadding, crossAxis: contextMenu }),
			size({
				padding: boundaryPadding,
				apply({ availableWidth, availableHeight, elements }) {
					if (contextMenu) {
						const viewport = elements.floating.ownerDocument.documentElement;
						const maxWidth = Math.max(
							0,
							viewport.clientWidth - boundaryPadding * 2,
						);
						const maxHeight = Math.max(
							0,
							viewport.clientHeight - boundaryPadding * 2,
						);
						elements.floating.style.maxWidth = `${maxWidth}px`;
						elements.floating.style.maxHeight = `${maxHeight}px`;
						elements.floating.style.overflow =
							elements.floating.scrollWidth > maxWidth ||
							elements.floating.scrollHeight > maxHeight
								? "auto"
								: "visible";
						return;
					}

					elements.floating.style.maxWidth = `${Math.max(0, availableWidth)}px`;
					elements.floating.style.maxHeight = `${Math.max(
						0,
						Math.min(availableHeight, maxHeight ?? Number.POSITIVE_INFINITY),
					)}px`;
					elements.floating.style.overflow = "auto";
				},
			}),
		],
	});

	useLayoutEffect(() => {
		setReference(
			(reference && isPoint(reference)
				? virtualReference
				: reference) as ReferenceType | null,
		);
	}, [reference, setReference, virtualReference]);

	const dismiss = useDismiss(context);
	const roleProps = useRole(context, { role });
	const { getFloatingProps } = useInteractions([dismiss, roleProps]);

	if (!open || !reference) return null;

	return (
		<FloatingPortal>
			<FloatingFocusManager
				context={context}
				modal={false}
				initialFocus={focusOnOpen ? 0 : -1}
				returnFocus={returnFocus}
			>
				<div
					ref={setFloating}
					aria-label={label}
					className={className}
					style={floatingStyles}
					{...getFloatingProps({
						onKeyDown: menuNavigation
							? (event: KeyboardEvent<HTMLDivElement>) =>
									moveMenuFocus(event.currentTarget, event)
							: undefined,
					})}
				>
					{children}
				</div>
			</FloatingFocusManager>
		</FloatingPortal>
	);
}

export function FloatingMenu(
	props: Omit<FloatingSurfaceProps, "role" | "focusOnOpen" | "menuNavigation">,
) {
	return <FloatingSurface {...props} role="menu" focusOnOpen menuNavigation />;
}
