import {
	Camera,
	Check,
	ChevronRight,
	Download,
	ExternalLink,
	Globe2,
	Loader2,
	PanelRightClose,
	PanelRightOpen,
	RefreshCw,
	Search,
	Send,
	ShieldCheck,
} from "lucide-react";
import {
	type FormEvent,
	useCallback,
	useEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import {
	connectBrowser,
	getBrowserPageInfo,
	getBrowserScreenshot,
	getBrowserSnapshot,
	getBrowserStatus,
	openBrowserPage,
	selectBrowser,
	type BrowserProvider,
} from "../api/browser";
import type { ServerMessage } from "../types/protocol";

interface Props {
	subscribe?: (handler: (message: ServerMessage) => void) => () => void;
	onSendToAgent: (text: string, image?: string) => Promise<boolean>;
}

function blobDataURL(blob: Blob): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result ?? ""));
		reader.onerror = () => reject(reader.error);
		reader.readAsDataURL(blob);
	});
}

function browserToolResult(
	message: ServerMessage,
	providers: BrowserProvider[],
): boolean {
	if (message.type !== "tool_result") return false;
	return providers.some(
		(provider) =>
			provider.connected &&
			!!provider.server &&
			(message.name === provider.server ||
				message.name.startsWith(`${provider.server}_`)),
	);
}

export function BrowserWorkspace({ subscribe, onSendToAgent }: Props) {
	const [providers, setProviders] = useState<BrowserProvider[]>([]);
	const [selected, setSelected] = useState("");
	const [loading, setLoading] = useState(true);
	const [connecting, setConnecting] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const [url, setURL] = useState("http://localhost:5173");
	const [info, setInfo] = useState("");
	const [snapshot, setSnapshot] = useState("");
	const [domQuery, setDOMQuery] = useState("");
	const [selectedDOM, setSelectedDOM] = useState("");
	const [screenshot, setScreenshot] = useState("");
	const [inspectorOpen, setInspectorOpen] = useState(true);
	const [request, setRequest] = useState("");
	const [sending, setSending] = useState(false);
	const refreshTimer = useRef<number | null>(null);

	const provider = useMemo(
		() => providers.find((candidate) => candidate.id === selected),
		[providers, selected],
	);
	const connected = providers.filter((candidate) => candidate.connected);
	const domLines = useMemo(() => {
		const query = domQuery.trim().toLowerCase();
		return snapshot
			.split("\n")
			.map((line) => line.trimEnd())
			.filter(
				(line) => line.trim() && (!query || line.toLowerCase().includes(query)),
			)
			.slice(0, 500);
	}, [domQuery, snapshot]);

	const loadStatus = useCallback(async () => {
		try {
			const status = await getBrowserStatus();
			setProviders(status.providers ?? []);
			setSelected((current) => status.selected || current);
			setError("");
		} catch (loadError) {
			setError(String(loadError));
		} finally {
			setLoading(false);
		}
	}, []);

	const loadPage = useCallback(async (providerID: string) => {
		if (!providerID) return;
		setBusy(true);
		setError("");
		try {
			const nextInfo = await getBrowserPageInfo(providerID);
			const nextSnapshot = await getBrowserSnapshot(providerID);
			const nextScreenshot = await blobDataURL(
				await getBrowserScreenshot(providerID),
			);
			setInfo(nextInfo);
			setSnapshot(nextSnapshot);
			setSelectedDOM((current) =>
				current && nextSnapshot.includes(current) ? current : "",
			);
			setScreenshot(nextScreenshot);
		} catch (loadError) {
			setError(
				loadError instanceof Error ? loadError.message : String(loadError),
			);
		} finally {
			setBusy(false);
		}
	}, []);

	useEffect(() => {
		// oxlint-disable-next-line react/set-state-in-effect -- the state updates occur only after the status request resolves.
		void loadStatus();
	}, [loadStatus]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type === "browser_changed") void loadStatus();
			if (!browserToolResult(message, providers)) return;
			if (refreshTimer.current !== null)
				window.clearTimeout(refreshTimer.current);
			refreshTimer.current = window.setTimeout(() => {
				refreshTimer.current = null;
				void loadPage(selected);
			}, 250);
		});
	}, [loadPage, loadStatus, providers, selected, subscribe]);

	useEffect(
		() => () => {
			if (refreshTimer.current !== null)
				window.clearTimeout(refreshTimer.current);
		},
		[],
	);

	const connect = async (candidate: BrowserProvider) => {
		setConnecting(candidate.id);
		setError("");
		try {
			await connectBrowser(candidate.id);
			await loadStatus();
			setSelected(candidate.id);
		} catch (connectError) {
			setError(
				connectError instanceof Error
					? connectError.message
					: String(connectError),
			);
		} finally {
			setConnecting("");
		}
	};

	const choose = async (providerID: string) => {
		const previous = selected;
		setSelected(providerID);
		setInfo("");
		setSnapshot("");
		setSelectedDOM("");
		setScreenshot("");
		try {
			await selectBrowser(providerID);
			await loadPage(providerID);
		} catch (selectError) {
			setSelected(previous);
			setError(String(selectError));
		}
	};

	const navigate = async (event: FormEvent) => {
		event.preventDefault();
		if (!selected || !url.trim()) return;
		setBusy(true);
		setError("");
		try {
			await openBrowserPage(selected, url.trim());
			await loadPage(selected);
		} catch (navigateError) {
			setError(
				navigateError instanceof Error
					? navigateError.message
					: String(navigateError),
			);
		} finally {
			setBusy(false);
		}
	};

	const sendToAgent = async () => {
		if (!request.trim() || sending) return;
		setSending(true);
		try {
			const image = screenshot || undefined;
			const context = [
				`Use the connected ${provider?.name ?? "browser"} MCP tools to work on the current browser page.`,
				`Request: ${request.trim()}`,
				info ? `\nCurrent page:\n${info}` : "",
				selectedDOM ? `\nSelected DOM target:\n${selectedDOM}` : "",
				snapshot
					? `\nLatest DOM/accessibility snapshot:\n${snapshot.slice(0, 24000)}`
					: "",
				"\nInspect the live page yourself, make the requested code changes, and verify the result in the browser.",
			].join("\n");
			if (await onSendToAgent(context, image)) setRequest("");
		} finally {
			setSending(false);
		}
	};

	const captureSelectedElement = async () => {
		const uid = selectedDOM.match(/\buid=([^\s]+)/)?.[1];
		if (!uid || selected !== "chrome") return;
		setBusy(true);
		setError("");
		try {
			setScreenshot(
				await blobDataURL(await getBrowserScreenshot(selected, uid)),
			);
		} catch (captureError) {
			setError(
				captureError instanceof Error
					? captureError.message
					: String(captureError),
			);
		} finally {
			setBusy(false);
		}
	};

	if (loading) {
		return (
			<div className="flex h-full items-center justify-center text-fg-dim">
				<Loader2 size={18} className="animate-spin" />
			</div>
		);
	}

	if (connected.length === 0) {
		return (
			<div className="h-full overflow-auto bg-bg p-6">
				<div className="mx-auto max-w-3xl">
					<div className="mb-6 flex items-start gap-3">
						<div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-accent/10 text-accent">
							<Globe2 size={17} />
						</div>
						<div>
							<h1 className="text-[15px] font-medium text-fg">
								Browser workspace
							</h1>
							<p className="mt-1 max-w-xl text-[12px] leading-5 text-fg-muted">
								Connect a maintained browser MCP server. Wingman keeps its DOM,
								debugging output, and screenshots beside your code and chat.
							</p>
						</div>
					</div>
					<div className="grid gap-3 md:grid-cols-2">
						{providers.map((candidate) => (
							<div
								key={candidate.id}
								className="flex min-h-52 flex-col rounded-xl border border-border bg-bg-surface p-4"
							>
								<div className="flex items-center gap-2">
									<Globe2 size={14} className="text-fg-dim" />
									<div className="text-[13px] font-medium text-fg">
										{candidate.name}
									</div>
								</div>
								<p className="mt-3 text-[11.5px] leading-5 text-fg-muted">
									{candidate.description}
								</p>
								{candidate.setup && (
									<p className="mt-3 text-[10.5px] leading-4 text-fg-dim">
										{candidate.setup}
									</p>
								)}
								<button
									type="button"
									disabled={!candidate.available || !!connecting}
									onClick={() => void connect(candidate)}
									className="mt-auto flex h-8 items-center justify-center gap-2 rounded-md border border-border-strong bg-bg px-3 text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
								>
									{connecting === candidate.id ? (
										<Loader2 size={12} className="animate-spin" />
									) : (
										<ChevronRight size={12} />
									)}
									{candidate.requires_download
										? "Download and connect"
										: "Connect"}
								</button>
							</div>
						))}
					</div>
					<div className="mt-5 flex items-center gap-2 text-[10.5px] text-fg-dim">
						<ShieldCheck size={12} />
						Browser profiles are isolated. Chrome telemetry and CrUX lookups are
						disabled.
					</div>
					{error && <div className="mt-4 text-[11px] text-danger">{error}</div>}
				</div>
			</div>
		);
	}

	return (
		<div className="flex h-full min-h-0 flex-col bg-bg">
			<div className="flex h-11 shrink-0 items-center gap-2 border-b border-border-subtle px-2">
				<div className="flex shrink-0 items-center rounded-md border border-border bg-bg-surface p-0.5">
					{connected.map((candidate) => (
						<button
							key={candidate.id}
							type="button"
							onClick={() => void choose(candidate.id)}
							className={`flex h-7 items-center gap-1.5 rounded px-2 text-[10.5px] transition-colors ${selected === candidate.id ? "bg-bg-active text-fg" : "text-fg-dim hover:text-fg-muted"}`}
						>
							{selected === candidate.id && <Check size={10} />}
							{candidate.name}
						</button>
					))}
				</div>
				<form
					onSubmit={(event) => void navigate(event)}
					className="flex min-w-0 flex-1"
				>
					<input
						value={url}
						onChange={(event) => setURL(event.target.value)}
						aria-label="Browser URL"
						spellCheck={false}
						className="h-8 min-w-0 flex-1 rounded-l-md border border-r-0 border-border bg-bg-surface px-2.5 text-[11px] text-fg outline-none focus:border-focus"
					/>
					<button
						type="submit"
						disabled={busy || !selected}
						className="flex h-8 w-8 items-center justify-center rounded-r-md border border-border bg-bg-surface text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-40"
						title="Open URL"
					>
						<ExternalLink size={12} />
					</button>
				</form>
				<button
					type="button"
					onClick={() => void loadPage(selected)}
					disabled={busy}
					className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-40"
					title="Refresh browser evidence"
				>
					<RefreshCw size={12} className={busy ? "animate-spin" : ""} />
				</button>
				{screenshot && (
					<a
						href={screenshot}
						download={`browser-${selected}.png`}
						className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim hover:bg-bg-hover hover:text-fg"
						title="Download screenshot"
					>
						<Download size={12} />
					</a>
				)}
				<button
					type="button"
					onClick={() => setInspectorOpen((open) => !open)}
					className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim hover:bg-bg-hover hover:text-fg"
					title={inspectorOpen ? "Hide DOM evidence" : "Show DOM evidence"}
				>
					{inspectorOpen ? (
						<PanelRightClose size={13} />
					) : (
						<PanelRightOpen size={13} />
					)}
				</button>
			</div>

			{error && (
				<div className="shrink-0 border-b border-danger/20 bg-danger/5 px-3 py-2 text-[10.5px] text-danger">
					{error}
				</div>
			)}

			<div className="flex min-h-0 flex-1">
				<div className="relative flex min-w-0 flex-1 items-center justify-center overflow-auto bg-bg-surface/40 p-4">
					{busy && (
						<div className="absolute top-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-full border border-border bg-bg-elevated/90 px-3 py-1.5 text-[10.5px] text-fg-muted shadow-lg backdrop-blur">
							<Loader2 size={11} className="animate-spin" /> Inspecting live
							page
						</div>
					)}
					{screenshot ? (
						<img
							src={screenshot}
							alt="Current browser page"
							className="max-h-full max-w-full rounded-md border border-border bg-white object-contain shadow-xl"
						/>
					) : (
						<div className="flex max-w-sm flex-col items-center text-center text-fg-dim">
							<div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full border border-border bg-bg">
								<Camera size={18} />
							</div>
							<div className="text-[12px] text-fg-muted">
								Open a page to capture its live state
							</div>
							<div className="mt-1 text-[10.5px] leading-4">
								Agent actions through {provider?.name ?? "the browser"} appear
								here after each tool call.
							</div>
						</div>
					)}
				</div>

				{inspectorOpen && (
					<aside className="flex w-[340px] min-w-[260px] max-w-[42%] shrink-0 flex-col border-l border-border-subtle bg-bg">
						<div className="min-h-0 flex-1 overflow-auto p-3">
							<div className="mb-2 text-[10px] font-medium tracking-wide text-fg-dim uppercase">
								Page
							</div>
							<pre className="whitespace-pre-wrap break-words font-mono text-[10.5px] leading-4 text-fg-muted">
								{info || "No page information captured yet."}
							</pre>
							<div className="mt-5 mb-2 text-[10px] font-medium tracking-wide text-fg-dim uppercase">
								DOM / accessibility snapshot
							</div>
							{snapshot ? (
								<>
									<div className="relative mb-2">
										<Search
											size={11}
											className="pointer-events-none absolute top-2 left-2 text-fg-dim"
										/>
										<input
											value={domQuery}
											onChange={(event) => setDOMQuery(event.target.value)}
											placeholder="Filter snapshot"
											className="h-7 w-full rounded-md border border-border bg-bg-surface pr-2 pl-7 text-[10.5px] text-fg outline-none placeholder:text-fg-dim focus:border-focus"
										/>
									</div>
									<div className="flex flex-col gap-px font-mono text-[10px] leading-4">
										{domLines.map((line, index) => (
											<button
												type="button"
												key={`${index}:${line}`}
												onClick={() => setSelectedDOM(line)}
												className={`rounded px-1.5 py-0.5 text-left whitespace-pre-wrap break-words ${selectedDOM === line ? "bg-accent/15 text-accent" : "text-fg-muted hover:bg-bg-hover hover:text-fg"}`}
											>
												{line}
											</button>
										))}
									</div>
									{domLines.length === 0 && (
										<div className="text-[10.5px] text-fg-dim">
											No matching snapshot lines.
										</div>
									)}
								</>
							) : (
								<div className="text-[10.5px] text-fg-dim">
									No snapshot captured yet.
								</div>
							)}
						</div>
						<div className="shrink-0 border-t border-border-subtle p-3">
							{selectedDOM && (
								<div className="mb-2 rounded-md border border-accent/20 bg-accent/5 p-2">
									<div className="text-[9.5px] font-medium tracking-wide text-accent uppercase">
										Selected target
									</div>
									<div className="mt-1 line-clamp-3 font-mono text-[10px] leading-4 text-fg-muted">
										{selectedDOM}
									</div>
									{selected === "chrome" &&
										/\buid=([^\s]+)/.test(selectedDOM) && (
											<button
												type="button"
												onClick={() => void captureSelectedElement()}
												className="mt-1.5 flex items-center gap-1.5 text-[10px] text-accent hover:text-accent/80"
											>
												<Camera size={10} /> Capture this element
											</button>
										)}
								</div>
							)}
							<label
								className="mb-1.5 block text-[10.5px] text-fg-muted"
								htmlFor="browser-agent-request"
							>
								Ask the agent about this state
							</label>
							<textarea
								id="browser-agent-request"
								value={request}
								onChange={(event) => setRequest(event.target.value)}
								onKeyDown={(event) => {
									if (
										(event.metaKey || event.ctrlKey) &&
										event.key === "Enter"
									) {
										event.preventDefault();
										void sendToAgent();
									}
								}}
								placeholder="Fix the mobile navigation and verify it…"
								className="h-20 w-full resize-none rounded-md border border-border bg-bg-surface p-2 text-[11px] text-fg outline-none placeholder:text-fg-dim focus:border-focus"
							/>
							<button
								type="button"
								onClick={() => void sendToAgent()}
								disabled={!request.trim() || sending}
								className="mt-2 flex h-8 w-full items-center justify-center gap-2 rounded-md bg-accent px-3 text-[11px] font-medium text-white hover:bg-accent/90 disabled:cursor-not-allowed disabled:opacity-40"
							>
								{sending ? (
									<Loader2 size={12} className="animate-spin" />
								) : (
									<Send size={12} />
								)}
								Send screenshot and DOM context
							</button>
						</div>
					</aside>
				)}
			</div>
		</div>
	);
}
