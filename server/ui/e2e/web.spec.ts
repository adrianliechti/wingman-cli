import { emptySession, sessionKey } from "../src/state/sessionStore.ts";
import {
	expect,
	test,
	type APIRequestContext,
	type Locator,
	type Page,
} from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { createHash } from "node:crypto";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";

test.use({
	extraHTTPHeaders: { "X-Wingman-Instance": process.env.E2E_INSTANCE ?? "" },
});

async function mockSavedSessions(
	page: Page,
	sessions: { id: string; title: string; updated_at: string }[],
	loaded: string[] = [],
	backend = "wingman",
) {
	const scope = await (await page.request.get("/api/v2/bootstrap")).json();
	await page.route(
		new RegExp(`/api/v2/backends/${backend}/sessions$`),
		async (route) => {
			if (route.request().method() === "GET")
				await route.fulfill({ json: sessions });
			else await route.continue();
		},
	);
	await page.routeWebSocket(/\/api\/v2\/events/, (socket) => {
		const server = socket.connectToServer();
		socket.onMessage((message) => {
			const command = JSON.parse(String(message));
			if (
				command.type === "subscribe" &&
				command.ref.backendId === backend &&
				sessions.some((session) => session.id === command.ref.sessionId)
			) {
				const id = command.ref.sessionId;
				if (!loaded.includes(id)) loaded.push(id);
				socket.send(
					JSON.stringify({
						type: "session.snapshot",
						subscriptionId: command.subscriptionId,
						ref: {
							workspaceId: scope.workspaceId,
							backendId: backend,
							sessionId: id,
						},
						epoch: `fixture:${id}`,
						revision: 0,
						entries: [],
						state: {
							...emptySession(sessionKey(backend, id)),
							status: "ready",
						},
					}),
				);
			} else server.send(message);
		});
	});
}
function sessionRow(page: Page, id: string) {
	return page.locator(
		`[data-session-id=${JSON.stringify(sessionKey("wingman", id))}]`,
	);
}
function sessionTab(page: Page, id: string) {
	return page.locator(
		`[data-center-tab=${JSON.stringify(`chat:${sessionKey("wingman", id)}`)}]`,
	);
}

function controlURL(): string {
	const url = process.env.E2E_CONTROL_URL;
	if (!url) throw new Error("E2E_CONTROL_URL is required");
	return url;
}

function workspacePath(path: string): string {
	const workspace = process.env.E2E_WORKSPACE;
	if (!workspace) throw new Error("E2E_WORKSPACE is required");
	return join(workspace, path);
}

async function composer(page: Page) {
	await page.goto("/");
	const input = page.getByPlaceholder("Message Wingman…");
	await expect(input).toBeVisible();
	return input;
}

async function openNewMenu(page: Page) {
	await page.getByRole("button", { name: "New", exact: true }).click();
	const menu = page.getByRole("menu", { name: "New", exact: true });
	await expect(menu).toBeVisible();
	return menu;
}

async function openNewTerminal(page: Page) {
	const menu = await openNewMenu(page);
	await menu.getByRole("menuitem", { name: "Terminal", exact: true }).click();
}

async function openNewChat(page: Page) {
	const menu = await openNewMenu(page);
	await menu.getByRole("menuitem", { name: "Chat", exact: true }).click();
}

async function openSessions(page: Page) {
	await page
		.getByRole("button", { name: "Show sessions", exact: true })
		.click();
	await expect(
		page.locator('[data-chat-auxiliary-panel][data-view="sessions"]'),
	).toBeVisible();
}

async function resetCompletionFile(request: APIRequestContext) {
	// Browser contexts are isolated, but these tests share the saved workspace.
	const original = await request.get("/api/files/read?path=completion.go");
	expect(original.ok()).toBeTruthy();
	const { revision } = await original.json();
	const reset = await request.post("/api/files/write", {
		data: { path: "completion.go", content: "package main\n", revision },
	});
	expect(reset.ok()).toBeTruthy();
}

async function openTabFixture(page: Page, file: RegExp, lineText: string) {
	await composer(page);
	await page.getByRole("treeitem", { name: file }).click();
	await expect(page.locator(".monaco-editor")).toBeVisible();
	const line = page.locator(".view-line", { hasText: lineText }).first();
	await expect(line).toBeVisible();
	await line.click();
	await page.keyboard.press("End");
}

async function setEditorTabCompletion(
	request: APIRequestContext,
	enabled: boolean,
) {
	const response = await request.post("/api/settings/editor.tab.completion", {
		data: { "editor.tab.completion": enabled },
	});
	expect(response.ok()).toBeTruthy();
}

async function openWorkspaceMenu(page: Page) {
	await page
		.getByRole("tree", { name: "Workspace files" })
		.dispatchEvent("contextmenu", { clientX: 32, clientY: 32 });
	const menu = page.getByRole("menu", { name: "Workspace actions" });
	await expect(menu).toBeVisible();
	return menu;
}

async function expectFloatingInViewport(
	page: Page,
	element: Locator,
	padding = 8,
) {
	const box = await element.boundingBox();
	const viewport = page.viewportSize();
	expect(box).not.toBeNull();
	expect(viewport).not.toBeNull();
	expect(box!.x).toBeGreaterThanOrEqual(padding - 1);
	expect(box!.y).toBeGreaterThanOrEqual(padding - 1);
	expect(box!.x + box!.width).toBeLessThanOrEqual(
		viewport!.width - padding + 1,
	);
	expect(box!.y + box!.height).toBeLessThanOrEqual(
		viewport!.height - padding + 1,
	);
	expect(
		await element.evaluate(
			(node) => !document.getElementById("root")?.contains(node),
		),
	).toBe(true);
}

async function expectFloatingNotScrollable(element: Locator) {
	expect(
		await element.evaluate((node) => {
			const style = getComputedStyle(node);
			return {
				overflowX: style.overflowX,
				overflowY: style.overflowY,
				fitsX: node.scrollWidth <= node.clientWidth,
				fitsY: node.scrollHeight <= node.clientHeight,
			};
		}),
	).toEqual({
		overflowX: "visible",
		overflowY: "visible",
		fitsX: true,
		fitsY: true,
	});
}

test("focuses the composer surface without outlining its textarea", async ({
	page,
}) => {
	await page.emulateMedia({ colorScheme: "dark" });
	const input = await composer(page);
	await input.click();
	await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
	await expect
		.poll(() =>
			input.evaluate(
				(element) =>
					getComputedStyle(element.closest("[data-chat-composer]")!)
						.borderColor,
			),
		)
		.toBe("rgb(64, 64, 64)");
	const styles = await input.evaluate((element) => {
		const textarea = getComputedStyle(element);
		const surface = getComputedStyle(element.closest("[data-chat-composer]")!);
		return {
			textareaBorder: textarea.borderWidth,
			textareaFieldSizing: textarea.getPropertyValue("field-sizing"),
			textareaOutline: textarea.outlineStyle,
			textareaShadow: textarea.boxShadow,
			surfaceBorder: surface.borderWidth,
			surfaceBorderColor: surface.borderColor,
			surfaceBackground: surface.backgroundColor,
			surfaceOutline: surface.outlineStyle,
			surfaceShadow: surface.boxShadow,
		};
	});
	expect(styles).toEqual({
		textareaBorder: "0px",
		textareaFieldSizing: "content",
		textareaOutline: "none",
		textareaShadow: "none",
		surfaceBorder: "1px",
		surfaceBorderColor: "rgb(64, 64, 64)",
		surfaceBackground: "rgb(24, 24, 24)",
		surfaceOutline: "none",
		surfaceShadow: "none",
	});
});

test("reveals scrollbars only for direct user scrolling", async ({ page }) => {
	await composer(page);
	const history = page.locator("[data-chat-history]");
	expect(
		await history.evaluate(
			(element) => element.scrollHeight <= element.clientHeight + 1,
		),
	).toBe(true);
	await history.evaluate((element) => {
		const filler = document.createElement("div");
		filler.style.height = "2000px";
		filler.dataset.scrollTestFiller = "true";
		element.append(filler);
		element.scrollTop = 100;
	});
	await expect(history).not.toHaveClass(/scrollbar-active/);
	await history.dispatchEvent("wheel", { deltaY: 20 });
	await expect(history).toHaveClass(/scrollbar-active/);
	await expect(history).not.toHaveClass(/scrollbar-active/, { timeout: 2_000 });
});

test("preserves the visible reply when a finished turn collapses its working steps", async ({
	page,
}) => {
	const id = "scroll-snapshot";
	const scope = await (await page.request.get("/api/v2/bootstrap")).json();
	const entries = [
		{ id: "previous-user", type: "user", content: "Earlier question" },
		{
			id: "previous-reply",
			type: "assistant",
			content: "Earlier reply.\n\n".repeat(30),
		},
		{ id: "current-user", type: "user", content: "Current question" },
		{
			id: "working-reply",
			type: "assistant",
			content: "Working through the details.\n\n".repeat(30),
		},
		{
			id: "visible-reply",
			type: "assistant",
			content: "Final response anchor.\n\n".repeat(30),
		},
	];
	let finishTurn = () => {
		throw new Error("Session has not been subscribed");
	};
	await page.route(/\/api\/v2\/backends\/wingman\/sessions$/, (route) =>
		route.fulfill({
			json: [
				{ id, title: "Scroll snapshot", updated_at: "2026-01-01T00:00:00Z" },
			],
		}),
	);
	await page.routeWebSocket(/\/api\/v2\/events/, (socket) => {
		const server = socket.connectToServer();
		socket.onMessage((message) => {
			const command = JSON.parse(String(message));
			if (command.type !== "subscribe" || command.ref.sessionId !== id) {
				server.send(message);
				return;
			}
			const sendSnapshot = (phase: "streaming" | "idle", revision: number) =>
				socket.send(
					JSON.stringify({
						type: "session.snapshot",
						subscriptionId: command.subscriptionId,
						ref: {
							workspaceId: scope.workspaceId,
							backendId: "wingman",
							sessionId: id,
						},
						epoch: `fixture:${id}`,
						revision,
						entries,
						state: {
							...emptySession(sessionKey("wingman", id)),
							status: "ready",
							phase,
						},
					}),
				);
			sendSnapshot("streaming", 0);
			finishTurn = () => sendSnapshot("idle", 1);
		});
	});

	await composer(page);
	await openSessions(page);
	await sessionRow(page, id).click();
	const history = page.locator("[data-chat-history]");
	const reply = page.locator('[data-entry-id="visible-reply"]');
	const working = page.locator('[data-entry-id="working-reply"]');
	await expect(working).toBeAttached();
	// Let the initial programmatic scroll guard expire before scrolling manually.
	await page.waitForTimeout(150);
	await history.evaluate((container) => {
		const reply = container.querySelector('[data-entry-id="visible-reply"]')!;
		container.scrollTop +=
			reply.getBoundingClientRect().top -
			container.getBoundingClientRect().top -
			96;
		container.dispatchEvent(new Event("scroll"));
	});
	const before = await reply.evaluate(
		(element) => element.getBoundingClientRect().top,
	);
	finishTurn();
	await expect(working).toHaveCount(0);
	await expect
		.poll(async () =>
			Math.abs(
				(await reply.evaluate(
					(element) => element.getBoundingClientRect().top,
				)) - before,
			),
		)
		.toBeLessThan(2);
});

test("keeps mixed background activity compact in the titlebar", async ({
	page,
}) => {
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				git_init: false,
				lsp: true,
				debug: false,
				tasks: false,
				terminal: true,
				tab: false,
				"editor.tab.completion": false,
				platform: "linux",
				workspace_name: "workspace",
				managed_tools: {
					state: "installing",
					phase: "updating",
					tool: "debugpy",
					label: "Python debugger",
					current: 2,
					total: 4,
				},
			},
		});
	});
	await page.route(/\/api\/lsp\/status$/, async (route) => {
		await route.fulfill({
			json: {
				analyzing: true,
				services: [
					{
						server: "custom-lsp",
						label: "A bespoke language",
						project: "packages/a-very-long-project-name",
						analyzing: true,
						operations: [
							{
								title: "Reading project symbols",
								message:
									"Resolving a dependency with a deliberately long display name",
								percentage: 42,
							},
						],
					},
				],
			},
		});
	});

	await composer(page);
	const beacon = page.locator("[data-activity-center] button");
	await expect(beacon).toHaveText("");
	await expect(beacon).toHaveAttribute("data-activity-summary", "running");
	await expect(beacon.locator("svg.animate-spin")).toHaveCount(1);
	const newButton = page.getByRole("button", { name: "New", exact: true });
	await expect(newButton).toBeVisible();
	const newButtonBox = await newButton.boundingBox();
	const notificationBox = await beacon.boundingBox();
	expect(
		newButtonBox && notificationBox
			? notificationBox.x >= newButtonBox.x + newButtonBox.width
			: false,
	).toBe(true);
	const beaconStyle = await beacon.evaluate((element) => {
		const style = getComputedStyle(element);
		return {
			background: style.backgroundColor,
			border: style.borderTopWidth,
			width: element.getBoundingClientRect().width,
		};
	});
	expect(beaconStyle).toEqual({
		background: "rgba(0, 0, 0, 0)",
		border: "0px",
		width: 32,
	});
	await beacon.click();
	const popup = page.getByRole("dialog", { name: "Workspace activity" });
	await expect(popup).toBeVisible();
	await expectFloatingInViewport(page, popup);
	await expect(popup.locator("[data-activity-state=running]")).toHaveCount(2);
	await expect(popup.locator("[data-activity-hint]")).toHaveCSS(
		"-webkit-line-clamp",
		"2",
	);
	const box = await popup.boundingBox();
	expect(box?.height ?? 999).toBeLessThan(240);
});

test("keeps the active tab visible as the tab strip fills", async ({
	page,
}) => {
	await composer(page);
	const tabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.getByRole("tab");
	const initialCount = await tabs.count();
	for (let index = 0; index < 6; index++) {
		await openNewTerminal(page);
		await expect(tabs).toHaveCount(initialCount + index + 1);
	}

	await expect
		.poll(async () => {
			const tabStrip = page.getByRole("tablist", { name: "Open tabs" });
			const strip = await tabStrip.boundingBox();
			const active = await tabStrip
				.locator('[role="tab"][aria-selected="true"]')
				.boundingBox();
			if (!strip || !active) return false;
			return (
				active.x >= strip.x - 1 &&
				active.x + active.width <= strip.x + strip.width + 1
			);
		})
		.toBe(true);

	const terminalTabs = page.locator('[data-center-tab^="terminal:"]');
	for (let remaining = await terminalTabs.count(); remaining > 0; remaining--) {
		await terminalTabs.first().click();
		await terminalTabs.first().press("Delete");
		await expect(terminalTabs).toHaveCount(remaining - 1);
	}
});

test("allows the last chat to close into an empty workspace", async ({
	page,
}) => {
	await composer(page);
	const tabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.getByRole("tab");
	const draft = tabs.first();
	const draftId = await draft.getAttribute("data-center-tab");
	expect(draftId).toBeTruthy();

	await expect(draft).toHaveAttribute("aria-label", /Agent/);
	await expect(
		page.getByRole("button", { name: /background tasks/i }),
	).toHaveCount(0);
	await draft.hover();
	await draft.locator("[data-tab-close]").click();
	await expect(page.locator(`[data-center-tab="${draftId}"]`)).toHaveCount(0);
	const empty = page.locator("[data-empty-workspace]");
	await expect(empty).toBeVisible();
	await expect(empty.locator("img")).toHaveCSS("opacity", "1");
	await expect(empty.locator("picture")).toHaveCSS("opacity", "0.2");

	const newMenu = await openNewMenu(page);
	await expect(
		newMenu.getByRole("menuitem", { name: "Text File", exact: true }),
	).toBeVisible();
	await newMenu.getByRole("menuitem", { name: "Chat", exact: true }).click();
	await expect(tabs).toHaveCount(1);
	await expect(page.getByPlaceholder("Message Wingman…")).toBeVisible();
});

test("moves tabs between the left and right pane groups", async ({ page }) => {
	const input = await composer(page);
	await page.getByRole("treeitem", { name: /editable\.txt/ }).click();
	const fileTab = page.locator('[data-center-tab="file:editable.txt"]');
	await expect(fileTab).toBeVisible();
	await expect(input).toBeHidden();

	await fileTab.click({ button: "right" });
	await page.getByRole("menuitem", { name: "Move Right" }).click();
	const leftStrip = page.getByRole("tablist", { name: "Open tabs" });
	const rightStrip = page.getByRole("tablist", { name: "Right pane tabs" });
	await expect(
		rightStrip.locator('[data-center-tab="file:editable.txt"]'),
	).toBeVisible();
	await expect(
		leftStrip.locator('[data-center-tab="file:editable.txt"]'),
	).toHaveCount(0);
	await expect(input).toBeVisible();
	const editorBox = await page.locator(".monaco-editor").first().boundingBox();
	const inputBox = await input.boundingBox();
	expect(editorBox && inputBox && editorBox.x > inputBox.x).toBe(true);
	const movedBox = await fileTab.boundingBox();
	expect(
		movedBox && editorBox ? Math.abs(movedBox.x - editorBox.x) : 999,
	).toBeLessThanOrEqual(32);

	// files opened while the right pane has focus join the right group
	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	const completionTab = rightStrip.locator(
		'[data-center-tab="file:completion.go"]',
	);
	await expect(completionTab).toBeVisible();
	await expect(
		leftStrip.locator('[data-center-tab="file:completion.go"]'),
	).toHaveCount(0);

	await completionTab.click({ button: "right" });
	await page.getByRole("menuitem", { name: "Close", exact: true }).click();
	await expect(completionTab).toHaveCount(0);

	await fileTab.click({ button: "right" });
	await page.getByRole("menuitem", { name: "Move Left" }).click();
	await expect(rightStrip).toHaveCount(0);
	await expect(
		leftStrip.locator('[data-center-tab="file:editable.txt"]'),
	).toBeVisible();
	await expect(input).toBeHidden();
});

test("reuses and promotes preview tabs while browsing files", async ({
	page,
}) => {
	await composer(page);
	const tabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.locator('[role="tab"][data-center-tab^="file:"]');
	const initialTabs = await tabs.count();
	const editableFile = page.getByRole("treeitem", { name: /editable\.txt/ });
	const completionFile = page.getByRole("treeitem", { name: /completion\.go/ });
	const themeFile = page.getByRole("treeitem", { name: /theme-preview\.html/ });
	const editableTab = page.locator('[data-center-tab="file:editable.txt"]');
	const completionTab = page.locator('[data-center-tab="file:completion.go"]');
	const themeTab = page.locator('[data-center-tab="file:theme-preview.html"]');

	await editableFile.click();
	await expect(editableTab).toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 1);

	await completionFile.click();
	await expect(editableTab).toHaveCount(0);
	await expect(completionTab).toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 1);

	const editor = page.locator(".monaco-editor");
	await expect(editor).toBeVisible();
	await editor.click();
	await expect(completionTab).not.toHaveAttribute("data-tab-preview", "true");

	await themeFile.click();
	await expect(themeTab).toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 2);
	await editableFile.click();
	await expect(themeTab).toHaveCount(0);
	await expect(editableTab).toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 2);

	await editableFile.dblclick();
	await expect(editableTab).not.toHaveAttribute("data-tab-preview", "true");
	await themeFile.click();
	await expect(themeTab).toHaveAttribute("data-tab-preview", "true");
	await themeTab.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Tab actions" })
		.getByRole("menuitem", { name: "Keep Open" })
		.click();
	await expect(themeTab).not.toHaveAttribute("data-tab-preview", "true");
});

test("reuses and promotes preview tabs while browsing sessions", async ({
	page,
}) => {
	const savedSessions = [
		{
			id: "preview-session-one",
			title: "Preview session one",
			updated_at: "2026-08-14T10:00:00Z",
		},
		{
			id: "preview-session-two",
			title: "Preview session two",
			updated_at: "2026-08-14T09:00:00Z",
		},
		{
			id: "preview-session-three",
			title: "Preview session three",
			updated_at: "2026-08-14T08:00:00Z",
		},
	];
	const loadRequests: string[] = [];
	await mockSavedSessions(page, savedSessions, loadRequests);

	const input = await composer(page);
	await openSessions(page);
	const tabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.getByRole("tab");
	const initialTabs = await tabs.count();
	const draftTab = page.locator('[data-center-tab^="draft:"]');
	const firstSession = sessionRow(page, "preview-session-one");
	const secondSession = sessionRow(page, "preview-session-two");
	const thirdSession = sessionRow(page, "preview-session-three");
	const firstTab = sessionTab(page, "preview-session-one");
	const secondTab = sessionTab(page, "preview-session-two");
	const thirdTab = sessionTab(page, "preview-session-three");

	await firstSession.click();
	await expect(firstTab).toHaveAttribute("data-tab-preview", "true");
	await expect(draftTab).toHaveCount(1);
	await expect(tabs).toHaveCount(initialTabs + 1);

	await secondSession.click();
	await expect(firstTab).toHaveCount(0);
	await expect(secondTab).toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 1);

	await input.click();
	await expect(secondTab).not.toHaveAttribute("data-tab-preview", "true");
	await thirdSession.dblclick();
	await expect(thirdTab).not.toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 2);

	await firstSession.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Actions for Preview session one" })
		.getByRole("menuitem", { name: "Open session" })
		.click();
	await expect(firstTab).not.toHaveAttribute("data-tab-preview", "true");
	await expect(tabs).toHaveCount(initialTabs + 3);
	await expect.poll(() => loadRequests.length).toBe(3);
});

test("does not restore a chat when the last session preview is replaced", async ({
	page,
}) => {
	await mockSavedSessions(page, [
		{
			id: "sole-session-preview",
			title: "Sole session preview",
			updated_at: "2026-08-14T10:00:00Z",
		},
	]);

	const input = await composer(page);
	await input.fill("render markdown");
	await input.press("Enter");
	const initialSession = page.locator('[data-center-tab^="draft:"]');
	await expect(initialSession).toBeVisible();
	await openSessions(page);
	const previewRow = sessionRow(page, "sole-session-preview");
	const previewTab = sessionTab(page, "sole-session-preview");
	await previewRow.click();
	await expect(previewTab).toHaveAttribute("data-tab-preview", "true");

	await initialSession.hover();
	await initialSession.locator("[data-tab-close]").click();
	await expect(initialSession).toHaveCount(0);
	await expect(previewTab).toHaveCount(1);

	await page.getByRole("treeitem", { name: /editable\.txt/ }).click();
	await expect(previewTab).toHaveCount(0);
	await expect(
		page
			.getByRole("tablist", { name: "Open tabs" })
			.getByRole("tab", { name: "Agent", exact: true }),
	).toHaveCount(0);
});

test("uses canonical product names for agents", async ({ page }) => {
	await page.route(/\/api\/v2\/bootstrap$/, async (route) => {
		const response = await route.fetch();
		const scope = await response.json();
		await route.fulfill({
			json: {
				...scope,
				backends: [
					{ id: "wingman", name: "Wingman" },
					...["claude", "codex", "copilot", "opencode", "pi"].map((id) => ({
						id,
						name: id,
					})),
				],
			},
		});
	});
	await page.route(/\/api\/v2\/backends\/claude\/settings$/, (route) =>
		route.fulfill({
			json: {
				models: [{ id: "claude-test", name: "Claude Test" }],
				model: "claude-test",
				modes: [{ id: "agent", name: "Agent" }],
				mode: "agent",
				efforts: ["default"],
				effort: "default",
				canDelete: true,
			},
		}),
	);
	await page.goto("/opencode");
	await expect(page.getByPlaceholder("Message OpenCode…")).toBeVisible();
	await page.keyboard.press("Control+k");
	const palette = page.getByRole("dialog", { name: "Command palette" });
	await expect(palette).toBeVisible();
	await palette
		.getByRole("combobox", { name: "Search commands" })
		.fill("new chat");
	for (const name of ["Claude", "Codex", "Copilot", "OpenCode", "Pi"]) {
		await expect(
			palette.getByRole("option", { name: `New Chat (${name})`, exact: true }),
		).toBeVisible();
	}
	await palette
		.getByRole("option", { name: "New Chat (Claude)", exact: true })
		.click();
	await expect(page.getByPlaceholder("Message Claude…")).toBeVisible();
	await expect(page.getByTitle("Mode: Agent")).toBeVisible();
	await expect(page.getByTitle("claude-test · default")).toBeVisible();
});

test("groups diagnostics by file and opens individual problems", async ({
	page,
}) => {
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				git_init: false,
				lsp: true,
				debug: false,
				tasks: false,
				terminal: true,
				platform: "linux",
				workspace_name: "workspace",
			},
		});
	});
	const diagnostics = [
		{
			path: "completion.go",
			line: 3,
			column: 5,
			severity: "error",
			message: "Undefined symbol",
			source: "gopls",
		},
		{
			path: "completion.go",
			line: 7,
			column: 2,
			severity: "warning",
			message: "Value is never used",
			source: "compiler",
		},
		{
			path: "nested/example.go",
			line: 2,
			column: 1,
			severity: "info",
			message: "Consider simplifying this expression",
			source: "gopls",
		},
	];
	await page.route(/\/api\/lsp\/diagnostics$/, async (route) => {
		if (route.request().method() === "POST") {
			await route.fulfill({ json: diagnostics });
			return;
		}
		await route.fulfill({
			json: {
				diagnostics,
				checked_files: 2,
				discovered_files: 2,
				discovery_truncated: false,
				unknown_files: 0,
				unavailable_servers: [],
				analyzing: false,
			},
		});
	});

	await composer(page);
	await page
		.getByRole("tablist", { name: "Side panel views" })
		.getByRole("tab", { name: "Inspect" })
		.click();
	await expect(page.getByText("Diagnostics", { exact: true })).toBeVisible();
	await expect(
		page.getByRole("tablist", { name: "Inspector views" }),
	).toHaveCount(0);
	const completionGroup = page.locator('[data-problem-file="completion.go"]');
	const nestedGroup = page.locator('[data-problem-file="nested/example.go"]');
	await expect(completionGroup.locator("[data-problem-entry]")).toHaveCount(2);
	await expect(nestedGroup.locator("[data-problem-entry]")).toHaveCount(1);
	await expect(completionGroup.getByText("L3:5")).toHaveCount(0);
	await expect(completionGroup.getByText("gopls")).toHaveCount(0);
	await expect(
		completionGroup.getByRole("button", { name: "Undefined symbol" }),
	).toHaveAttribute("title", "Undefined symbol · completion.go:3:5 · gopls");

	await completionGroup.getByText("Undefined symbol").click();
	await expect(
		page.locator('[data-center-tab="file:completion.go"]'),
	).toHaveAttribute("data-tab-preview", "true");
});

test("uses each Git status slot for its stage action", async ({ page }) => {
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: true,
				lsp: false,
				tasks: false,
				terminal: true,
			},
		});
	});
	await page.route(/\/api\/git\/status$/, async (route) => {
		await route.fulfill({
			json: {
				branch: "main",
				ahead: 0,
				behind: 0,
				has_remote: false,
				files: [
					{
						path: "src/staged.ts",
						index_status: "M",
						worktree_status: "",
						staged: true,
						changed: false,
					},
					{
						path: "src/changed.ts",
						index_status: "",
						worktree_status: "M",
						staged: false,
						changed: true,
					},
				],
			},
		});
	});
	await page.route(/\/api\/git\/(?:branches|fetch)$/, async (route) => {
		await route.fulfill({
			json: {
				branches: [{ name: "main", current: true, remote: "" }],
				warning: "",
			},
		});
	});
	await composer(page);
	await page
		.getByRole("tablist", { name: "Side panel views" })
		.getByRole("tab", { name: "Changes", exact: true })
		.click();

	await page.getByTitle("main", { exact: true }).click();
	const branches = page.getByRole("dialog", { name: "Git branches" });
	await expect(branches).toBeVisible();
	await expectFloatingInViewport(page, branches);
	await page.keyboard.press("Escape");
	await expect(branches).toBeHidden();

	for (const entry of [
		{ path: "src/staged.ts", action: "Unstage" },
		{ path: "src/changed.ts", action: "Stage" },
	]) {
		const row = page.locator(`[data-change-row="${entry.path}"]`);
		const status = row.locator("[data-change-status]");
		const action = row.locator("[data-change-action]");
		const directory = row.locator("[data-change-directory]");
		const statusBox = await status.boundingBox();

		await expect(row).toBeVisible();
		await expect(status).toHaveText("M");
		await expect(status).toBeVisible();
		await expect(action).not.toBeVisible();
		await expect(
			row.getByRole("button", { name: `${entry.action} ${entry.path}` }),
		).toBeVisible();
		await expect(row.locator("[data-change-content]")).toHaveCSS(
			"padding-right",
			"12px",
		);

		await row.hover();
		await expect(status).not.toBeVisible();
		await expect(action).toBeVisible();
		await expect(directory).toBeVisible();
		const actionBox = await action.boundingBox();
		expect(statusBox).not.toBeNull();
		expect(actionBox).not.toBeNull();
		expect(
			Math.abs(
				actionBox!.x +
					actionBox!.width / 2 -
					(statusBox!.x + statusBox!.width / 2),
			),
		).toBeLessThanOrEqual(1);
	}

	const changedRow = page.locator('[data-change-row="src/changed.ts"]');
	await changedRow.click({ button: "right" });
	const menu = page.getByRole("menu", {
		name: "Actions for src/changed.ts",
	});
	await expect(menu).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Open Changes" }),
	).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Open File" })).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Stage Changes" }),
	).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Discard Changes" }),
	).toBeVisible();
	await expect(
		page.getByRole("dialog", { name: "Discard changes?" }),
	).toHaveCount(0);
	await expectFloatingInViewport(page, menu);
	await menu.getByRole("menuitem", { name: "Discard Changes" }).click();
	await expect(
		page.getByRole("dialog", { name: "Discard changes?" }),
	).toBeVisible();
});

test("generates staged commit messages without overwriting concurrent edits", async ({
	page,
}) => {
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: true,
				lsp: false,
				tasks: false,
				terminal: true,
			},
		});
	});
	await page.route(/\/api\/git\/status$/, async (route) => {
		await route.fulfill({
			json: {
				branch: "main",
				ahead: 0,
				behind: 0,
				has_remote: false,
				files: [
					{
						path: "src/staged.ts",
						index_status: "M",
						worktree_status: "",
						staged: true,
						changed: false,
					},
				],
			},
		});
	});
	let generated = 0;
	let releaseSecond!: () => void;
	let secondStarted!: () => void;
	const waitForSecond = new Promise<void>((resolve) => {
		secondStarted = resolve;
	});
	const secondRelease = new Promise<void>((resolve) => {
		releaseSecond = resolve;
	});
	const firstMessage =
		"Generated staged message 1\n\nExplain the staged behavior with enough detail to wrap in the narrow commit footer.";
	await page.route(/\/api\/git\/commit-message$/, async (route) => {
		generated++;
		if (generated === 2) {
			secondStarted();
			await secondRelease;
		}
		await route.fulfill({
			json: {
				message:
					generated === 1
						? firstMessage
						: `Generated staged message ${generated}`,
			},
		});
	});
	let commits = 0;
	await page.route(/\/api\/git\/commit$/, async (route) => {
		commits++;
		await route.fulfill({ json: { output: "Committed" } });
	});

	await composer(page);
	await page
		.getByRole("tablist", { name: "Side panel views" })
		.getByRole("tab", { name: "Changes", exact: true })
		.click();
	const message = page.getByRole("textbox", { name: "Commit message" });
	const generate = page.getByRole("button", {
		name: "Generate commit message",
	});
	await generate.click();
	await expect(message).toHaveValue(firstMessage);
	expect(
		await message.evaluate((element) => ({
			tag: element.tagName,
			height: element.getBoundingClientRect().height,
		})),
	).toEqual({ tag: "TEXTAREA", height: expect.any(Number) });
	expect((await message.boundingBox())!.height).toBeGreaterThan(28);
	await expect(generate).toBeHidden();
	const commit = page.getByRole("button", { name: "Commit", exact: true });
	await expect(commit).toBeVisible();
	const messageBox = await message.boundingBox();
	const commitBox = await commit.boundingBox();
	expect(messageBox).not.toBeNull();
	expect(commitBox).not.toBeNull();
	expect(commitBox!.y).toBeGreaterThanOrEqual(
		messageBox!.y + messageBox!.height - 1,
	);
	expect(commits).toBe(0);

	await message.fill("");
	await expect(generate).toBeVisible();
	await generate.click();
	await waitForSecond;
	await message.fill("Keep my hand-written message");
	releaseSecond();
	await expect(message).toHaveValue("Keep my hand-written message");
	await expect(
		page.getByText(
			"Generated message was not inserted because the commit box changed.",
			{ exact: true },
		),
	).toBeVisible();
	expect(commits).toBe(0);
});

test("compares branches and commits in a main content tab", async ({
	page,
}) => {
	const pageErrors: Error[] = [];
	const consoleErrors: string[] = [];
	page.on("pageerror", (error) => pageErrors.push(error));
	page.on("console", (message) => {
		if (message.type() === "error") consoleErrors.push(message.text());
	});
	const baseHash = "1111111111111111111111111111111111111111";
	const middleHash = "3333333333333333333333333333333333333333";
	const headHash = "2222222222222222222222222222222222222222";
	const archivedHash = (index: number) =>
		`${(index + 4).toString(16).padStart(7, "0")}${"a".repeat(33)}`;
	const olderCommits = Array.from({ length: 200 }, (_, index) => ({
		hash: archivedHash(index),
		parents: index === 199 ? [] : [archivedHash(index + 1)],
		summary: `Archived commit ${index + 1}`,
		author: "Ada",
		authored_at: "2026-08-12T10:00:00Z",
		refs: [],
	}));
	const compareModes: string[] = [];
	const compareBases: string[] = [];
	const compareHeads: string[] = [];
	const historyQueries: string[] = [];
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: true,
				lsp: false,
				tasks: false,
				terminal: true,
			},
		});
	});
	await page.route(/\/api\/git\/status$/, async (route) => {
		await route.fulfill({
			json: {
				branch: "feature",
				ahead: 1,
				behind: 0,
				has_remote: false,
				files: [],
			},
		});
	});
	await page.route(/\/api\/git\/(?:branches|fetch)$/, async (route) => {
		await route.fulfill({
			json: {
				branches: [{ name: "feature", current: true }, { name: "main" }],
			},
		});
	});
	await page.route(/\/api\/git\/history/, async (route) => {
		historyQueries.push(new URL(route.request().url()).search);
		await route.fulfill({
			json: [
				{
					hash: headHash,
					parents: [middleHash],
					summary: "Feature work",
					author: "Ada",
					authored_at: "2026-08-14T10:00:00Z",
					refs: ["feature"],
				},
				{
					hash: middleHash,
					parents: [baseHash],
					summary: "Unreferenced work",
					author: "Ada",
					authored_at: "2026-08-13T12:00:00Z",
					refs: null,
				},
				{
					hash: baseHash,
					parents: [olderCommits[0].hash],
					summary: "Initial commit",
					author: "Ada",
					authored_at: "2026-08-13T10:00:00Z",
					refs: ["main"],
				},
				...olderCommits,
			],
		});
	});
	await page.route(/\/api\/git\/compare/, async (route) => {
		const request = new URL(route.request().url());
		compareModes.push(request.searchParams.get("mode") || "");
		compareBases.push(request.searchParams.get("base") || "");
		compareHeads.push(request.searchParams.get("head") || "");
		await route.fulfill({
			json: {
				base: request.searchParams.get("base"),
				head: request.searchParams.get("head"),
				base_hash:
					request.searchParams.get("base") === ":empty" ? "" : baseHash,
				head_hash: headHash,
				...(request.searchParams.get("mode") === "merge-base"
					? { merge_base_hash: baseHash }
					: {}),
				files: Array.from({ length: 80 }, (_, index) => {
					if (index === 1) {
						return {
							path: "dist/bundle.min.js",
							status: "modified",
							patch:
								"--- a/dist/bundle.min.js\n+++ b/dist/bundle.min.js\n@@ -1 +1 @@\n-old\n+new\n",
							language: "javascript",
						};
					}
					if (index === 2) {
						return {
							path: "src/deleted.ts",
							status: "deleted",
							patch:
								"--- a/src/deleted.ts\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n",
							language: "typescript",
						};
					}
					if (index === 3) {
						return {
							path: "src/renamed.ts",
							original_path: "src/original.ts",
							status: "modified",
							patch:
								"--- a/src/original.ts\n+++ b/src/renamed.ts\n@@ -1 +1 @@\n-old\n+new\n",
							language: "typescript",
						};
					}
					if (index === 4) {
						return {
							path: "src/moved.ts",
							original_path: "src/movable.ts",
							status: "modified",
							patch: "diff --git a/src/movable.ts b/src/moved.ts\n",
							language: "typescript",
						};
					}
					return {
						path: `src/feature-${index}.ts`,
						status: "modified",
						patch: `--- a/src/feature-${index}.ts\n+++ b/src/feature-${index}.ts\n@@ -1 +1 @@\n-old\n+new\n`,
						language: "typescript",
					};
				}),
			},
		});
	});

	await composer(page);
	await page
		.getByRole("tablist", { name: "Side panel views" })
		.getByRole("tab", { name: "Changes", exact: true })
		.click();

	await page.getByTitle("feature", { exact: true }).click();
	const branchDialog = page.getByRole("dialog", { name: "Git branches" });
	await expect(branchDialog).toBeVisible();
	await branchDialog
		.getByRole("button", { name: "Compare main with working tree" })
		.click();
	await expect(
		page.getByRole("tab", { name: /main → Working tree/ }),
	).toBeVisible();
	await expect(
		page.getByText("Pull-request comparison from merge base"),
	).toHaveCount(0);
	const virtualList = page.locator("[data-virtual-compare-list]");
	await expect(virtualList).toBeVisible();
	await expect(
		page.locator('[data-compare-file="src/feature-0.ts"]'),
	).toBeVisible();
	const firstFileHeader = page.locator(
		'[data-compare-file="src/feature-0.ts"] button',
	);
	await expect(firstFileHeader.getByText("+1", { exact: true })).toBeVisible();
	await expect(firstFileHeader.getByText("−1", { exact: true })).toBeVisible();
	await expect(
		page.locator('[data-compare-file="dist/bundle.min.js"] button'),
	).toHaveAttribute("aria-expanded", "false");
	await expect(
		page.locator('[data-compare-file="src/deleted.ts"]'),
	).toHaveAttribute("data-summary-only", "true");
	await expect(
		page.locator('[data-compare-file="src/renamed.ts"] button'),
	).toHaveAttribute("aria-expanded", "true");
	await expect(
		page.locator('[data-compare-file="src/moved.ts"]'),
	).toHaveAttribute("data-summary-only", "true");
	await expect
		.poll(() => page.locator("[data-compare-file]").count())
		.toBeLessThan(10);
	await expect(virtualList.locator(".monaco-diff-editor")).toHaveCount(0);
	for (let index = 0; index < 20; index++) {
		await virtualList.evaluate(
			(element, scrollToEnd) => {
				element.scrollTop = scrollToEnd ? element.scrollHeight : 0;
				element.dispatchEvent(new Event("scroll"));
			},
			index % 2 === 0,
		);
		await page.waitForTimeout(10);
	}
	await virtualList.evaluate((element) => {
		element.scrollTop = element.scrollHeight;
		element.dispatchEvent(new Event("scroll"));
	});
	await expect(page.locator('[data-active-file-header="true"]')).toHaveCount(1);
	await expect(page.locator('[data-active-file-header="true"]')).toBeVisible();
	await virtualList.evaluate((element) => {
		element.scrollTop = 0;
		element.dispatchEvent(new Event("scroll"));
	});
	await virtualList.evaluate((element) => {
		const nextRow = element
			.querySelector<HTMLElement>('[data-compare-file="dist/bundle.min.js"]')
			?.closest<HTMLElement>("[data-index]");
		if (!nextRow) throw new Error("Expected the next comparison row");
		const nextRowStart =
			nextRow.getBoundingClientRect().top -
			element.getBoundingClientRect().top +
			element.scrollTop;
		element.scrollTop = nextRowStart + 1;
		element.dispatchEvent(new Event("scroll"));
	});
	const nextFileHeader = page.locator(
		'[data-compare-file="dist/bundle.min.js"]',
	);
	await expect(nextFileHeader).toHaveCount(1);
	await expect(nextFileHeader).toBeVisible();
	await expect(nextFileHeader).toHaveAttribute(
		"data-active-file-header",
		"true",
	);
	await expect
		.poll(async () => {
			const listBox = await virtualList.boundingBox();
			const headerBox = await nextFileHeader.boundingBox();
			if (!listBox || !headerBox) return Number.POSITIVE_INFINITY;
			return Math.abs(headerBox.y - listBox.y);
		})
		.toBeLessThanOrEqual(1);
	await expect
		.poll(() =>
			virtualList.evaluate((element) => {
				const top = element.getBoundingClientRect().top;
				return Array.from(
					element.querySelectorAll<HTMLElement>("[data-compare-file]"),
				).filter(
					(header) => Math.abs(header.getBoundingClientRect().top - top) <= 1,
				).length;
			}),
		)
		.toBe(1);

	const historyToggle = page.getByRole("button", {
		name: "History",
		exact: true,
	});
	await historyToggle.click();
	await expect(historyToggle).toHaveText("History");
	await expect(page.getByText("Unreferenced work")).toBeVisible();
	await expect.poll(() => historyQueries.length).toBeGreaterThan(0);
	expect(
		historyQueries.every((query) => !new URLSearchParams(query).has("limit")),
	).toBe(true);
	const historyList = page.locator("[data-virtual-git-history]");
	await expect(historyList).toBeVisible();
	const historyResize = page.getByRole("separator", {
		name: "Resize Git history",
	});
	const historyBeforeResize = await historyList.boundingBox();
	const historyResizeBox = await historyResize.boundingBox();
	expect(historyBeforeResize).not.toBeNull();
	expect(historyResizeBox).not.toBeNull();
	await page.mouse.move(
		historyResizeBox!.x + historyResizeBox!.width / 2,
		historyResizeBox!.y + historyResizeBox!.height / 2,
	);
	await page.mouse.down();
	await page.mouse.move(
		historyResizeBox!.x + historyResizeBox!.width / 2,
		historyResizeBox!.y - 60,
	);
	await page.mouse.up();
	await expect
		.poll(async () => (await historyList.boundingBox())?.height ?? 0)
		.toBeGreaterThan(historyBeforeResize!.height + 40);
	await expect
		.poll(() => page.locator("[data-git-commit]").count())
		.toBeLessThan(30);
	await historyList.evaluate((element) => {
		element.scrollTop = element.scrollHeight;
		element.dispatchEvent(new Event("scroll"));
	});
	const rootCommit = page.locator(
		`[data-git-commit="${olderCommits[olderCommits.length - 1].hash}"]`,
	);
	await expect(rootCommit).toBeVisible();
	await rootCommit.click();
	await expect(rootCommit.locator("[data-commit-select]")).toHaveAttribute(
		"aria-pressed",
		"false",
	);
	const centerTabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.getByRole("tab");
	const rootPreview = page.getByRole("tab", {
		name: /Empty tree → 00000cb/,
	});
	await expect(rootPreview).toHaveAttribute("data-tab-preview", "true");
	const tabsWithRootPreview = await centerTabs.count();
	await historyList.evaluate((element) => {
		element.scrollTop = 0;
		element.dispatchEvent(new Event("scroll"));
	});
	await expect(page.getByText("Unreferenced work")).toBeVisible();
	const middleCommit = page.locator(`[data-git-commit="${middleHash}"]`);
	await middleCommit.click({ button: "right" });
	const commitMenu = page.getByRole("menu", {
		name: "Actions for Unreferenced work",
	});
	await expect(commitMenu).toBeVisible();
	await expect(
		commitMenu.getByRole("menuitem", { name: "Open commit changes" }),
	).toBeVisible();
	await expect(
		commitMenu.getByRole("menuitem", { name: "Copy commit hash" }),
	).toBeVisible();
	const commitMenuBox = await commitMenu.boundingBox();
	await page.keyboard.down("Alt");
	await expect(
		commitMenu.getByRole("menuitem", { name: "Copy short hash" }),
	).toBeVisible();
	await expect.poll(() => commitMenu.boundingBox()).toEqual(commitMenuBox);
	await page.keyboard.up("Alt");
	await page.keyboard.press("Escape");
	await middleCommit.click();
	const middlePreview = page.getByRole("tab", { name: /1111111 → 3333333/ });
	await expect(middlePreview).toHaveAttribute("data-tab-preview", "true");
	await expect(rootPreview).toHaveCount(0);
	await expect(centerTabs).toHaveCount(tabsWithRootPreview);
	await middleCommit.dblclick();
	await expect(middlePreview).not.toHaveAttribute("data-tab-preview", "true");
	const baseCommit = page.locator(`[data-git-commit="${baseHash}"]`);
	const baseSelect = baseCommit.locator("[data-commit-select]");
	await baseSelect.click();
	const basePreview = page.getByRole("tab", { name: /0000004 → 1111111/ });
	await expect(basePreview).toHaveAttribute("data-tab-preview", "true");
	await expect(baseSelect).toHaveAttribute("aria-pressed", "true");
	await expect(baseSelect).toHaveText("C");
	await expect(centerTabs).toHaveCount(tabsWithRootPreview + 1);
	const headCommit = page.locator(`[data-git-commit="${headHash}"]`);
	const headSelect = headCommit.locator("[data-commit-select]");
	await headSelect.click();
	const headPreview = page.getByRole("tab", { name: /1111111 → 2222222/ });
	await expect(headPreview).toHaveAttribute("data-tab-preview", "true");
	await expect(baseSelect).toHaveText("B");
	await expect(headSelect).toHaveText("C");
	await expect(basePreview).toHaveCount(0);
	await expect(centerTabs).toHaveCount(tabsWithRootPreview + 1);
	await headSelect.click();
	await expect(headSelect).toHaveAttribute("aria-pressed", "false");
	await expect(baseSelect).toHaveText("C");
	await expect(basePreview).toHaveAttribute("data-tab-preview", "true");
	await middleCommit.click();
	await expect(middlePreview).toHaveAttribute("aria-selected", "true");
	await expect(baseSelect).toHaveText("C");
	await baseSelect.click();
	await expect(baseSelect).toHaveAttribute("aria-pressed", "false");
	await expect(basePreview).toHaveAttribute("aria-selected", "true");
	await page.getByText("80 changed files", { exact: true }).click();
	await expect(basePreview).not.toHaveAttribute("data-tab-preview", "true");
	await expect
		.poll(() => compareModes)
		.toEqual([
			"merge-base",
			"direct",
			"direct",
			"direct",
			"direct",
			"direct",
			"direct",
			"direct",
		]);
	await expect
		.poll(() => compareBases)
		.toEqual([
			"main",
			":empty",
			baseHash,
			olderCommits[0].hash,
			baseHash,
			olderCommits[0].hash,
			baseHash,
			olderCommits[0].hash,
		]);
	await expect
		.poll(() => compareHeads)
		.toEqual([
			":worktree",
			olderCommits[olderCommits.length - 1].hash,
			middleHash,
			baseHash,
			headHash,
			baseHash,
			middleHash,
			baseHash,
		]);
	// Leave time for delayed browser errors to reach the listeners.
	await page.waitForTimeout(500);
	expect(pageErrors).toEqual([]);
	expect(consoleErrors).toEqual([]);
});

test("shows and copies diagnostics for comparison failures", async ({
	page,
	context,
}) => {
	await context.grantPermissions(["clipboard-read", "clipboard-write"]);
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: true,
				lsp: false,
				tasks: false,
				terminal: true,
			},
		});
	});
	await page.route(/\/api\/git\/status$/, async (route) => {
		await route.fulfill({
			json: {
				branch: "feature/router",
				ahead: 1,
				behind: 0,
				has_remote: false,
				files: [],
			},
		});
	});
	await page.route(/\/api\/git\/(?:branches|fetch)$/, async (route) => {
		await route.fulfill({
			json: {
				branches: [{ name: "feature/router", current: true }, { name: "main" }],
			},
		});
	});
	await page.route(/\/api\/git\/compare/, async (route) => {
		await route.fulfill({
			status: 409,
			contentType: "text/plain",
			body: "read tree entry examples/coding-router.yaml: directory not found",
		});
	});

	await composer(page);
	await page
		.getByRole("tablist", { name: "Side panel views" })
		.getByRole("tab", { name: "Changes", exact: true })
		.click();
	await page.getByTitle("feature/router", { exact: true }).click();
	await page
		.getByRole("dialog", { name: "Git branches" })
		.getByRole("button", { name: "Compare main with working tree" })
		.click();

	await expect(
		page.getByRole("alert", { name: "This tab stopped rendering" }),
	).toBeVisible();
	await page.getByRole("button", { name: "Copy error" }).click();
	await expect(page.getByRole("button", { name: "Copied" })).toBeVisible();
	const copiedError = await page.evaluate(() => navigator.clipboard.readText());
	expect(copiedError).toContain("Operation: Compare Git revisions");
	expect(copiedError).toContain("Target: :worktree");
	expect(copiedError).toContain("Response: HTTP 409");
	expect(copiedError).toContain("Client stack:");
	expect(copiedError).toContain(
		"Server response:\nread tree entry examples/coding-router.yaml",
	);
});

test("keeps the session context menu above panel clipping", async ({
	page,
}) => {
	await page.route(/\/api\/v2\/backends\/wingman\/sessions$/, async (route) => {
		await route.fulfill({
			json: [
				{
					id: "menu-clipping-check",
					title: "Menu clipping check",
					updated_at: "2026-08-11T00:00:00Z",
				},
			],
		});
	});
	await page.route(/\/api\/v2\/backends\/wingman\/settings$/, async (route) => {
		await route.fulfill({
			json: {
				models: [],
				model: "",
				modes: [],
				mode: "",
				efforts: [],
				effort: "",
				canDelete: true,
			},
		});
	});
	await composer(page);
	await openSessions(page);

	const session = page.getByTitle("Menu clipping check");
	await expect(
		page.getByRole("button", {
			name: "Session actions for Menu clipping check",
		}),
	).toHaveCount(0);
	await session.click({ button: "right" });

	const menu = page.getByRole("menu", {
		name: "Actions for Menu clipping check",
	});
	await expect(menu).toBeVisible();
	await expectFloatingInViewport(page, menu);
});

test("shows file actions above panel clipping", async ({ page }) => {
	await page.setViewportSize({ width: 640, height: 400 });
	await composer(page);
	const file = page.getByRole("treeitem", { name: /editable\.txt/ });
	await file.dispatchEvent("contextmenu", { clientX: 160, clientY: 200 });

	const menu = page.getByRole("menu", { name: "Actions for editable.txt" });
	await expect(menu).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "New File…" })).toHaveCount(
		0,
	);
	await expect(menu.getByRole("menuitem", { name: "New Folder…" })).toHaveCount(
		0,
	);
	await expect(menu.getByRole("menuitem", { name: "Cut" })).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Copy", exact: true }),
	).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Paste" })).toBeDisabled();
	await expect(menu.getByRole("menuitem", { name: "Copy Path" })).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Copy Relative Path" }),
	).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: /Reveal in/ })).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Copy Contents" }),
	).toHaveCount(0);
	await expect(menu.getByRole("menuitem", { name: "Download" })).toHaveCount(0);
	await expect(menu.getByRole("menuitem", { name: "Rename" })).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Duplicate" })).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Delete" })).toBeVisible();
	await expectFloatingInViewport(page, menu, 4);
	await expectFloatingNotScrollable(menu);
	await expect(menu.getByRole("menuitem", { name: "Open" })).toBeFocused();
	await page.keyboard.press("ArrowDown");
	await expect(menu.getByRole("menuitem", { name: "Cut" })).toBeFocused();

	await page.keyboard.press("Escape");
	await page
		.getByRole("treeitem", { name: "nested" })
		.click({ button: "right" });
	const folderMenu = page.getByRole("menu", { name: "Actions for nested" });
	await expect(
		folderMenu.getByRole("menuitem", { name: "New File…" }),
	).toBeVisible();
	await expect(
		folderMenu.getByRole("menuitem", { name: "New Folder…" }),
	).toBeVisible();

	await page.keyboard.press("Escape");
	const workspaceMenu = await openWorkspaceMenu(page);
	await expect(
		workspaceMenu.getByRole("menuitem", { name: "New File…" }),
	).toBeVisible();
	await expect(
		workspaceMenu.getByRole("menuitem", { name: "New Folder…" }),
	).toBeVisible();
});

test("creates, saves, refreshes, and protects files changed on disk", async ({
	page,
	request,
}) => {
	await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
	await composer(page);
	let workspaceMenu = await openWorkspaceMenu(page);
	await workspaceMenu.getByRole("menuitem", { name: "New File…" }).click();
	const nameInput = page.getByRole("textbox", {
		name: "New file name in workspace",
	});
	await nameInput.fill("web-created.go");
	await nameInput.press("Enter");

	await expect(
		page.getByRole("tab", { name: /web-created\.go/ }),
	).toBeVisible();
	const editor = page.locator(".monaco-editor");
	await expect(editor).toBeVisible();
	await editor.click();
	await page.keyboard.insertText("package main\n\nfunc initialVersion() {}\n");
	const createdTab = page.getByRole("tab", { name: /web-created\.go/ });
	await expect(createdTab).toHaveAttribute("aria-label", /unsaved changes/);
	await page.keyboard.press("ControlOrMeta+S");
	await expect(createdTab).not.toHaveAttribute("aria-label", /unsaved changes/);

	let read = await request.get("/api/files/read?path=web-created.go");
	expect(read.ok()).toBeTruthy();
	let file = (await read.json()) as { content: string; revision: string };
	expect(file.content).toContain("initialVersion");

	const refreshedContent = "package main\n\nfunc refreshedFromDisk() {}\n";
	await writeFile(workspacePath("web-created.go"), refreshedContent);
	await expect(page.locator(".view-lines")).toContainText("refreshedFromDisk");

	await editor.click();
	await page.keyboard.press("Control+End");
	await page.keyboard.insertText("\nfunc localVersion() {}\n");
	await expect(page.locator(".view-lines")).toContainText("localVersion");
	await writeFile(
		workspacePath("web-created.go"),
		"package main\n\nfunc newerDiskVersion() {}\n",
	);
	const conflictBanner = page.getByText(
		"This file changed on disk while you had unsaved edits.",
	);
	await expect(conflictBanner).toBeVisible();
	await writeFile(workspacePath("web-created.go"), refreshedContent);
	await expect(conflictBanner).not.toBeVisible();
	await writeFile(
		workspacePath("web-created.go"),
		"package main\n\nfunc newerDiskVersion() {}\n",
	);
	await expect(conflictBanner).toBeVisible();

	await editor.click();
	await page.keyboard.press("ControlOrMeta+S");
	const overwriteDialog = page.getByRole("dialog", {
		name: "Overwrite newer file?",
	});
	await expect(overwriteDialog).toBeVisible();
	await overwriteDialog.getByRole("button", { name: "Overwrite" }).click();
	await expect(overwriteDialog).not.toBeVisible();

	read = await request.get("/api/files/read?path=web-created.go");
	file = (await read.json()) as { content: string; revision: string };
	expect(file.content).toContain("localVersion");
	expect(file.content).not.toContain("newerDiskVersion");

	await page
		.getByRole("treeitem", { name: "nested" })
		.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Actions for nested" })
		.getByRole("menuitem", { name: "New File…" })
		.click();
	const nestedName = page.getByRole("textbox", {
		name: "New file name in nested",
	});
	await nestedName.fill("child.go");
	await nestedName.press("Enter");
	await expect(page.getByRole("tab", { name: /child\.go/ })).toBeVisible();
	const nestedRead = await request.get(
		"/api/files/read?path=nested%2Fchild.go",
	);
	expect(nestedRead.ok()).toBeTruthy();

	workspaceMenu = await openWorkspaceMenu(page);
	await workspaceMenu.getByRole("menuitem", { name: "New Folder…" }).click();
	const folderName = page.getByRole("textbox", {
		name: "New folder name in workspace",
	});
	await folderName.fill("web-folder");
	await folderName.press("Enter");
	const folder = page.getByRole("treeitem", { name: "web-folder" });
	await expect(folder).toBeVisible();

	await page
		.getByRole("treeitem", { name: /web-created\.go/ })
		.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Actions for web-created.go" })
		.getByRole("menuitem", { name: "Cut" })
		.click();
	await folder.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Actions for web-folder" })
		.getByRole("menuitem", { name: "Paste" })
		.click();

	await folder.click();
	const movedFile = page.getByRole("treeitem", { name: /web-created\.go/ });
	await expect(movedFile).toBeVisible();
	await movedFile.click({ button: "right" });
	let fileMenu = page.getByRole("menu", { name: "Actions for web-created.go" });
	await fileMenu.getByRole("menuitem", { name: "Copy Relative Path" }).click();
	await expect
		.poll(() => page.evaluate(() => navigator.clipboard.readText()))
		.toBe("web-folder/web-created.go");

	await movedFile.click({ button: "right" });
	fileMenu = page.getByRole("menu", { name: "Actions for web-created.go" });
	await fileMenu.getByRole("menuitem", { name: "Copy Path" }).click();
	await expect
		.poll(() => page.evaluate(() => navigator.clipboard.readText()))
		.toBe(workspacePath("web-folder/web-created.go"));

	await movedFile.click();
	await expect(
		page.getByRole("tab", { name: /web-created\.go/ }),
	).toHaveAttribute("aria-selected", "true");
	await editor.click();
	await page.keyboard.press("Control+End");
	await page.keyboard.type("\nfunc savedAfterMove() {}\n");
	await page.keyboard.press("ControlOrMeta+S");
	await expect(
		page.getByRole("tab", { name: /web-created\.go/ }),
	).not.toHaveAttribute("aria-label", /unsaved changes/);
	const movedRead = await request.get(
		"/api/files/read?path=web-folder%2Fweb-created.go",
	);
	expect(movedRead.ok()).toBeTruthy();
	expect(((await movedRead.json()) as { content: string }).content).toContain(
		"savedAfterMove",
	);
	const oldRead = await request.get("/api/files/read?path=web-created.go");
	expect(oldRead.status()).toBe(404);

	const originalFile = page
		.getByRole("treeitem", { name: /editable\.txt/ })
		.first();
	await originalFile.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Actions for editable.txt" })
		.getByRole("menuitem", { name: "Copy", exact: true })
		.click();
	await folder.click({ button: "right" });
	await page
		.getByRole("menu", { name: "Actions for web-folder" })
		.getByRole("menuitem", { name: "Paste" })
		.click();

	const originalCopySource = await request.get(
		"/api/files/read?path=editable.txt",
	);
	const copiedFile = await request.get(
		"/api/files/read?path=web-folder%2Feditable.txt",
	);
	expect(originalCopySource.ok()).toBeTruthy();
	expect(copiedFile.ok()).toBeTruthy();
	expect(((await copiedFile.json()) as { content: string }).content).toBe(
		((await originalCopySource.json()) as { content: string }).content,
	);
});

test("uses Monaco's live buffer for automatic completion and parameter hints", async ({
	page,
}) => {
	let completionContent = "";
	let signatureContent = "";
	await page.route(/\/api\/lsp\/completions$/, async (route) => {
		const body = route.request().postDataJSON() as { content?: string };
		completionContent = body.content ?? "";
		await route.fulfill({
			json: {
				isIncomplete: false,
				items: [
					{ label: "Done", kind: 2, detail: "func()", insertText: "Done" },
				],
			},
		});
	});
	await page.route(/\/api\/lsp\/signature-help$/, async (route) => {
		const body = route.request().postDataJSON() as { content?: string };
		signatureContent = body.content ?? "";
		await route.fulfill({
			json: {
				signatures: [
					{
						label: "consume(name string, count int)",
						parameters: [{ label: "name string" }, { label: "count int" }],
					},
				],
				activeSignature: 0,
				activeParameter: 0,
			},
		});
	});

	await composer(page);
	const capabilitiesResponse = page.waitForResponse(/\/api\/lsp\/capabilities/);
	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	await capabilitiesResponse;
	const editor = page.locator(".monaco-editor");
	await expect(editor).toBeVisible();
	await editor.click({ button: "right" });
	const editorMenu = page.getByRole("menu", { name: "Editor actions" });
	await expect(
		editorMenu.getByRole("menuitem", { name: "Go to Definition" }),
	).toBeEnabled();
	await page.keyboard.press("Escape");
	await editor.click();
	await page.keyboard.press("ControlOrMeta+A");
	await page.keyboard.type("ctx.");
	await expect.poll(() => completionContent).toContain("ctx.");
	await expect(page.locator(".suggest-widget:visible")).toContainText("Done");

	await page.keyboard.press("Escape");
	await page.keyboard.press("ControlOrMeta+A");
	await page.keyboard.type("consume(");
	await expect.poll(() => signatureContent).toContain("consume(");
	await expect(page.locator(".parameter-hints-widget:visible")).toContainText(
		"consume(name string, count int)",
	);
});

test("syntax-highlights shell scripts without a language server", async ({
	page,
}) => {
	await composer(page);
	await page.getByRole("treeitem", { name: /syntax-highlight\.sh/ }).click();
	const editor = page.locator(".monaco-editor");
	await expect(editor).toBeVisible();
	await expect
		.poll(async () =>
			editor.locator(".view-lines").evaluate((lines) => {
				const tokenClasses = new Set<string>();
				for (const element of lines.querySelectorAll<HTMLElement>("span")) {
					for (const name of element.classList) {
						if (/^mtk\d+$/.test(name)) tokenClasses.add(name);
					}
				}
				return tokenClasses.size;
			}),
		)
		.toBeGreaterThan(1);
});

test("leaves TypeScript project diagnostics to the active language server", async ({
	page,
}) => {
	await page.route(/\/api\/lsp\/capabilities\?/, async (route) => {
		await route.fulfill({
			json: {
				workspace_uri: "file:///workspace",
				language_server: true,
			},
		});
	});
	await page.route(/\/api\/lsp\/diagnostics$/, async (route) => {
		await route.fulfill({ json: [] });
	});

	await composer(page);
	await page
		.getByRole("treeitem", { name: /standalone-diagnostics\.tsx/ })
		.click();
	const editor = page.locator(".monaco-editor");
	await expect(editor).toBeVisible();
	await expect(
		editor.locator(".squiggly-error, .squiggly-warning"),
	).toHaveCount(0);
});

test("shows one project-aware TypeScript hover", async ({ page }) => {
	let hoverRequests = 0;
	await page.route(/\/api\/lsp\/capabilities\?/, async (route) => {
		await route.fulfill({
			json: {
				workspace_uri: "file:///workspace",
				language_server: true,
				hover: true,
			},
		});
	});
	await page.route(/\/api\/lsp\/diagnostics$/, async (route) => {
		await route.fulfill({ json: [] });
	});
	await page.route(/\/api\/lsp\/hover$/, async (route) => {
		hoverRequests++;
		await route.fulfill({
			json: {
				contents: '```typescript\nconst view: "Hello"\n```',
			},
		});
	});

	await composer(page);
	await page
		.getByRole("treeitem", { name: /standalone-diagnostics\.tsx/ })
		.click();
	const line = page.locator(".view-line", { hasText: "const view" });
	await expect(line).toBeVisible();
	await line.hover({ position: { x: 48, y: 8 } });
	const hover = page.locator(".monaco-hover:visible");
	await expect(hover).toContainText('const view: "Hello"');
	await expect.poll(() => hoverRequests).toBe(1);
	await expect(hover.locator(".hover-row")).toHaveCount(1);
});

test("does not flash standalone diagnostics while the project LSP starts", async ({
	page,
}) => {
	let capabilitiesRequested = false;
	let releaseCapabilities = () => {};
	const capabilitiesReady = new Promise<void>((resolve) => {
		releaseCapabilities = resolve;
	});
	await page.route(/\/api\/lsp\/capabilities\?/, async (route) => {
		capabilitiesRequested = true;
		await capabilitiesReady;
		await route.fulfill({
			json: {
				workspace_uri: "file:///workspace",
				language_server: true,
			},
		});
	});
	await page.route(/\/api\/lsp\/diagnostics$/, async (route) => {
		await route.fulfill({ json: [] });
	});

	await composer(page);
	await page
		.getByRole("treeitem", { name: /standalone-diagnostics\.tsx/ })
		.click();
	const diagnostics = page
		.locator(".monaco-editor")
		.locator(".squiggly-error, .squiggly-warning");
	try {
		await expect.poll(() => capabilitiesRequested).toBe(true);
		await page.waitForTimeout(1000);
		expect(await diagnostics.count()).toBe(0);
	} finally {
		releaseCapabilities();
	}
	await expect(diagnostics).toHaveCount(0);
});

test("upgrades an open editor when its language server becomes available", async ({
	page,
}) => {
	let languageServer = false;
	let toolsState: "installing" | "ready" = "installing";
	let editorCapabilityRequests = 0;
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				git_init: false,
				lsp: languageServer,
				debug: false,
				tasks: false,
				terminal: false,
				tab: false,
				"editor.tab.completion": false,
				platform: "linux",
				workspace_name: "workspace",
				managed_tools: { state: toolsState },
			},
		});
	});
	await page.route(/\/api\/lsp\/capabilities\?/, async (route) => {
		editorCapabilityRequests++;
		await route.fulfill({
			json: {
				workspace_uri: "file:///workspace",
				language_server: languageServer,
				definition: languageServer,
			},
		});
	});
	await page.route(/\/api\/lsp\/diagnostics$/, async (route) => {
		await route.fulfill({ json: [] });
	});

	await composer(page);
	await page
		.getByRole("treeitem", { name: /standalone-diagnostics\.tsx/ })
		.click();
	await expect.poll(() => editorCapabilityRequests).toBe(1);

	languageServer = true;
	toolsState = "ready";
	await expect
		.poll(() => editorCapabilityRequests, { timeout: 10_000 })
		.toBeGreaterThan(1);
	await page.locator(".monaco-editor").click({ button: "right" });
	await expect(
		page
			.getByRole("menu", { name: "Editor actions" })
			.getByRole("menuitem", { name: "Go to Definition" }),
	).toBeEnabled();
});

test("save awaits LSP actions that add and remove Go imports", async ({
	page,
	request,
}) => {
	let workspaceURI = "";
	const sourceActions: string[] = [];
	await page.route(/\/api\/lsp\/capabilities\?/, async (route) => {
		const response = await route.fetch();
		const capabilities = (await response.json()) as Record<string, unknown>;
		workspaceURI = String(capabilities.workspace_uri ?? "");
		await route.fulfill({
			response,
			json: {
				...capabilities,
				language_server: true,
				code_actions: true,
			},
		});
	});
	await page.route(/\/api\/lsp\/code-actions$/, async (route) => {
		const body = route.request().postDataJSON() as {
			content: string;
			only?: string[];
		};
		const kind = body.only?.[0] ?? "";
		if (kind) sourceActions.push(kind);
		const documentURI = `${workspaceURI.replace(/\/$/, "")}/completion.go`;
		const edit =
			kind === "source.addMissingImports"
				? {
						changes: {
							[documentURI]: [
								{
									range: {
										start: { line: 2, character: 0 },
										end: { line: 2, character: 0 },
									},
									newText: 'import "fmt"\n',
								},
							],
						},
					}
				: {
						changes: {
							[documentURI]: [
								{
									range: {
										start: { line: 2, character: 0 },
										end: { line: 4, character: 0 },
									},
									newText: 'import "fmt"\n',
								},
							],
						},
					};
		await route.fulfill({
			json: {
				actions: [
					{
						title: kind,
						kind,
						edit,
					},
				],
				documents: {
					[documentURI]: {
						path: "completion.go",
						revision: createHash("sha256").update(body.content).digest("hex"),
						exists: true,
					},
				},
			},
		});
	});

	await composer(page);
	const capabilitiesResponse = page.waitForResponse(/\/api\/lsp\/capabilities/);
	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	await capabilitiesResponse;
	const editor = page.locator(".monaco-editor").first();
	await expect(editor).toBeVisible();
	await editor.click();
	await page.keyboard.press("ControlOrMeta+A");
	await page.keyboard.insertText(
		'package main\n\nimport "os"\n\nfunc main() { fmt.Println("ok") }\n',
	);
	await page.keyboard.press("ControlOrMeta+S");
	await expect
		.poll(() => sourceActions)
		.toEqual(["source.addMissingImports", "source.organizeImports"]);
	const read = await request.get("/api/files/read?path=completion.go");
	const saved = (await read.json()) as { content: string };
	expect(saved.content).toContain('import "fmt"');
	expect(saved.content).not.toContain('import "os"');
});

test("applies one command-driven quick action without moving the cursor", async ({
	page,
	request,
}) => {
	await resetCompletionFile(request);
	let workspaceURI = "";
	let invokedRequests = 0;
	let commandExecutions = 0;
	await page.route(/\/api\/lsp\/capabilities\?/, async (route) => {
		const response = await route.fetch();
		const capabilities = (await response.json()) as Record<string, unknown>;
		workspaceURI = String(capabilities.workspace_uri ?? "");
		await route.fulfill({
			response,
			json: {
				...capabilities,
				language_server: true,
				code_actions: true,
			},
		});
	});
	await page.route(/\/api\/lsp\/diagnostics$/, async (route) => {
		await route.fulfill({
			json: [
				{
					path: "completion.go",
					line: 1,
					column: 1,
					end_line: 1,
					end_column: 8,
					severity: "warning",
					message: "Generated marker is missing",
					source: "test",
				},
			],
		});
	});
	await page.route(/\/api\/lsp\/code-actions$/, async (route) => {
		const body = route.request().postDataJSON() as {
			content: string;
			trigger_kind?: number;
			only?: string[];
		};
		if (body.only?.length) {
			await route.fulfill({ json: { actions: [], documents: {} } });
			return;
		}
		if (body.trigger_kind === 1) invokedRequests++;
		await route.fulfill({
			json: {
				actions: [
					{
						title: "Add generated marker",
						kind: "quickfix",
						command: {
							title: "Add generated marker",
							command: "applyModCommand",
							arguments: [],
						},
					},
				],
				documents: {},
			},
		});
	});
	await page.route(/\/api\/lsp\/execute-command$/, async (route) => {
		commandExecutions++;
		const body = route.request().postDataJSON() as { content: string };
		const documentURI = `${workspaceURI.replace(/\/$/, "")}/completion.go`;
		await route.fulfill({
			json: {
				edits: [
					{
						label: "Add generated marker",
						edit: {
							changes: {
								[documentURI]: [
									{
										range: {
											start: { line: 0, character: 0 },
											end: { line: 0, character: 0 },
										},
										newText: "// generated\n",
									},
								],
							},
						},
						documents: {
							[documentURI]: {
								path: "completion.go",
								revision: createHash("sha256")
									.update(body.content)
									.digest("hex"),
								exists: true,
							},
						},
					},
				],
			},
		});
	});

	await composer(page);
	const capabilitiesResponse = page.waitForResponse(/\/api\/lsp\/capabilities/);
	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	await capabilitiesResponse;
	const editor = page.locator(".monaco-editor").first();
	const packageLine = editor.locator(".view-line", { hasText: "package main" });
	await expect(packageLine).toBeVisible();
	await expect(editor.locator(".squiggly-warning")).toBeVisible();
	await packageLine.click();
	await page.keyboard.press("End");
	await page.keyboard.press("ControlOrMeta+.");
	await expect.poll(() => invokedRequests).toBe(1);
	const actionRows = page
		.locator(".action-widget:visible .monaco-list-row.action")
		.filter({ hasText: "Add generated marker" });
	await expect(actionRows).toHaveCount(1);
	await page.keyboard.press("Enter");
	await expect(editor.locator(".view-lines")).toContainText("// generated");
	expect(commandExecutions).toBe(1);
	await page.keyboard.insertText("X");
	await expect(editor.locator(".view-lines")).toContainText("package mainX");
	await expect(page.getByRole("tab", { name: /completion\.go/ })).toHaveCount(
		1,
	);
	await expect(
		page.getByRole("tab", { name: /completion\.go/ }),
	).toHaveAttribute("aria-label", /unsaved changes/);

	const beforeSave = await request.get("/api/files/read?path=completion.go");
	expect(((await beforeSave.json()) as { content: string }).content).toBe(
		"package main\n",
	);
	await page.keyboard.press("ControlOrMeta+S");
	await expect(
		page.getByRole("tab", { name: /completion\.go/ }),
	).not.toHaveAttribute("aria-label", /unsaved changes/);
	const afterSave = await request.get("/api/files/read?path=completion.go");
	expect(((await afterSave.json()) as { content: string }).content).toBe(
		"// generated\npackage mainX\n",
	);
});

test("save applies real gopls organize-imports edits", async ({
	page,
	request,
}) => {
	const capabilitiesResponse = await request.get(
		"/api/lsp/capabilities?path=organize-imports.go",
	);
	const capabilities = (await capabilitiesResponse.json()) as {
		language_server?: boolean;
		code_actions?: boolean;
	};
	test.skip(
		!capabilities.language_server || !capabilities.code_actions,
		"gopls is not available in this environment",
	);

	await composer(page);
	await page.getByRole("treeitem", { name: /organize-imports\.go/ }).click();
	const editor = page.locator(".monaco-editor").first();
	await expect(editor).toBeVisible();
	await editor.click();
	await page.keyboard.press("ControlOrMeta+A");
	await page.keyboard.insertText(
		'package main\n\nimport "os"\n\nfunc main() { fmt.Println("ok") }\n',
	);
	await page.keyboard.press("ControlOrMeta+S");
	await expect
		.poll(async () => {
			const response = await request.get(
				"/api/files/read?path=organize-imports.go",
			);
			return ((await response.json()) as { content: string }).content;
		})
		.toBe(
			'package main\n\nimport "fmt"\n\nfunc main() { fmt.Println("ok") }\n',
		);
});

test("toggles editor.tab.completion from the command palette", async ({
	page,
	request,
}) => {
	const initial = await request.get("/api/capabilities");
	expect(initial.ok()).toBeTruthy();
	expect(
		((await initial.json()) as Record<string, unknown>)[
			"editor.tab.completion"
		],
	).toBe(true);
	await composer(page);
	await page.keyboard.press("Control+k");
	const commandInput = page.getByRole("combobox", { name: "Search commands" });
	const palette = page.getByRole("dialog", { name: "Command palette" });
	await commandInput.evaluate((element) =>
		element.setAttribute("data-mount-marker", "palette-input"),
	);
	await palette.evaluate((element) =>
		element.setAttribute("data-mount-marker", "palette"),
	);
	await expect(commandInput).toHaveAttribute("autocorrect", "off");
	await expect(commandInput).toHaveAttribute("autocapitalize", "none");
	await expect(commandInput).toHaveAttribute("spellcheck", "false");
	await commandInput.pressSequentially("tab completion");
	await expect(commandInput).toHaveValue("tab completion");
	await expect(commandInput).toHaveAttribute(
		"data-mount-marker",
		"palette-input",
	);
	await expect(palette).toHaveAttribute("data-mount-marker", "palette");
	const disable = page.getByRole("option", {
		name: /Disable Tab Completion/,
	});
	await expect(disable).toBeVisible();
	await disable.click();
	await expect(
		page.getByText("Tab Completion disabled", { exact: true }),
	).toBeVisible();

	await page.keyboard.press("Control+k");
	await expect(
		page.getByRole("option", { name: /Enable Tab Completion/ }),
	).toBeVisible();
});

test("Tab propagates a recent rename through the real model endpoint", async ({
	page,
	request,
}) => {
	await setEditorTabCompletion(request, true);
	await openTabFixture(page, /tab-effectiveness\.go/, 'userName := "Ada"');

	let predictionRequests = 0;
	const predictionBodies: Array<{
		line: number;
		column: number;
		version: number;
		content: string;
		previous_content: string;
	}> = [];
	const isTabRequest = (candidate: { method(): string; url(): string }) =>
		candidate.method() === "POST" &&
		new URL(candidate.url()).pathname === "/api/editor/tab";
	page.on("request", (candidate) => {
		if (!isTabRequest(candidate)) return;
		predictionRequests++;
		predictionBodies.push(candidate.postDataJSON());
	});
	const tabRequest = page.waitForRequest(isTabRequest);
	const tabResponse = page.waitForResponse(
		(response) =>
			response.request().method() === "POST" &&
			new URL(response.url()).pathname === "/api/editor/tab",
	);
	const declaration = page
		.locator(".view-line", { hasText: 'userName := "Ada"' })
		.first();
	await declaration.click();
	await page.keyboard.press("Home");
	for (let index = 0; index < "userName".length; index++) {
		await page.keyboard.press("Shift+ArrowRight");
	}
	await page.keyboard.insertText("accountName");

	const observed = await tabRequest;
	const body = observed.postDataJSON() as {
		content: string;
		previous_content: string;
	};
	expect(body.previous_content).toContain('userName := "Ada"');
	expect(body.content).toContain('accountName := "Ada"');
	expect(body.content).toContain("println(userName)");
	const response = (await (await tabResponse).json()) as {
		edit: unknown;
	};
	expect(response.edit).toEqual({
		insert_text: "account",
		expected_text: "user",
		range: {
			start_line: 7,
			start_column: 13,
			end_line: 7,
			end_column: 17,
		},
	});

	await page.waitForTimeout(200);
	await page.keyboard.press("Tab");
	const correctedUse = page
		.locator(".view-line:visible", { hasText: "println(accountName)" })
		.first();
	// Monaco either accepts immediately or first focuses its long-distance
	// preview. In the latter presentation, the next Tab accepts the same item.
	if (!(await correctedUse.isVisible())) await page.keyboard.press("Tab");
	await expect(correctedUse).toBeVisible();
	await expect(
		page.locator(".view-line:visible", { hasText: "println(userName)" }),
	).toHaveCount(0);
	// Acceptance is the end of this edit burst: Monaco may ask its providers
	// again for the changed model, but that must not purchase another result.
	await page.waitForTimeout(700);
	expect(
		predictionRequests,
		`Tab prediction requests: ${JSON.stringify(predictionBodies)}`,
	).toBe(1);

	await page.keyboard.press("ControlOrMeta+S");
	await expect
		.poll(async () => {
			const saved = await request.get(
				"/api/files/read?path=tab-effectiveness.go",
			);
			return ((await saved.json()) as { content: string }).content;
		})
		.toContain("println(accountName)");
});

test("Tab stays idle until an edit and accepts a cursor completion", async ({
	page,
	request,
}) => {
	await setEditorTabCompletion(request, true);
	let predictions = 0;
	await page.route(/\/api\/editor\/tab$/, async (route) => {
		predictions++;
		const body = route.request().postDataJSON() as {
			line: number;
			column: number;
			version: number;
		};
		await route.fulfill({
			json: {
				version: body.version,
				edit: {
					insert_text: "quantity",
					expected_text: "",
					range: {
						start_line: body.line,
						start_column: body.column,
						end_line: body.line,
						end_column: body.column,
					},
				},
			},
		});
	});

	await openTabFixture(page, /tab-ghost\.go/, "total := price *");
	await page.waitForTimeout(400);
	expect(predictions).toBe(0);
	const synchronized = await request.post("/api/files/write", {
		data: {
			path: "tab-ghost.go",
			content:
				"package main\n\nfunc main() {\n\ttotal := price *\n\t_ = total\n}\n\n// synchronized externally\n",
			force: true,
		},
	});
	expect(synchronized.ok()).toBeTruthy();
	await expect(
		page.locator(".view-line", { hasText: "synchronized externally" }),
	).toBeVisible();
	await page.waitForTimeout(500);
	expect(predictions).toBe(0);
	await page.locator(".view-line", { hasText: "total := price *" }).click();
	await page.keyboard.press("End");

	await page.keyboard.type(" ");
	await page.keyboard.press("Backspace");
	await page.waitForTimeout(400);
	expect(predictions).toBe(0);

	await page.keyboard.type(" ");
	const ghost = page.locator(".ghost-text-decoration", {
		hasText: "quantity",
	});
	await expect(ghost).toBeVisible();
	await page.keyboard.press("Tab");
	await expect(
		page.locator(".view-line", { hasText: "price * quantity" }).first(),
	).toBeVisible();
	await page.waitForTimeout(700);
	expect(predictions).toBe(1);
});

test("Tab renders and accepts a real multiline Monaco inline edit", async ({
	page,
	request,
}) => {
	await setEditorTabCompletion(request, true);
	await page.route(/\/api\/editor\/tab$/, async (route) => {
		const body = route.request().postDataJSON() as { version: number };
		await route.fulfill({
			json: {
				version: body.version,
				edit: {
					insert_text: "if ready {\n  new one\n  new two\n}\n",
					expected_text: "old one \nold two\n",
					range: {
						start_line: 2,
						start_column: 1,
						end_line: 4,
						end_column: 1,
					},
				},
			},
		});
	});

	await openTabFixture(page, /tab-multiline\.txt/, "old one");
	await page.keyboard.type(" ");
	const preview = page.locator(".monaco-editor:visible").filter({
		hasText: "new one",
	});
	await expect(preview.first()).toBeVisible();
	await expect
		.poll(() => page.locator(".monaco-editor:visible").count())
		.toBeGreaterThan(1);
	await page.keyboard.press("Tab");
	await expect
		.poll(() => page.locator(".monaco-editor:visible").count())
		.toBe(1);
	await expect(
		page.locator(".view-line", { hasText: "if ready" }).first(),
	).toBeVisible();
	await expect(page.locator(".view-line", { hasText: "old one" })).toHaveCount(
		0,
	);
});

test("Tab drops a delayed response after the cursor moves", async ({
	page,
	request,
}) => {
	await setEditorTabCompletion(request, true);
	let firstStarted!: () => void;
	const started = new Promise<void>((resolve) => {
		firstStarted = resolve;
	});
	await page.route(/\/api\/editor\/tab$/, async (route) => {
		const body = route.request().postDataJSON() as {
			line: number;
			column: number;
			version: number;
		};
		const first = body.line === 1;
		if (first) {
			firstStarted();
			await new Promise((resolve) => setTimeout(resolve, 900));
		}
		try {
			await route.fulfill({
				json: {
					version: body.version,
					edit: {
						insert_text: first ? "oldValue" : " newValue",
						expected_text: "",
						range: {
							start_line: body.line,
							start_column: body.column,
							end_line: body.line,
							end_column: body.column,
						},
					},
				},
			});
		} catch {
			// The cursor move is expected to abort the first routed request.
		}
	});

	await openTabFixture(page, /tab-stale\.txt/, "first :=");
	await page.keyboard.type(" ");
	await started;
	const second = page.locator(".view-line", { hasText: "second :=" }).first();
	await second.click();
	await page.keyboard.press("End");
	const currentGhost = page.locator(".ghost-text-decoration", {
		hasText: "newValue",
	});
	await expect(currentGhost).toBeVisible();
	await page.waitForTimeout(1_000);
	await expect(currentGhost).toBeVisible();
	await expect(
		page.locator(".ghost-text-decoration", { hasText: "oldValue" }),
	).toHaveCount(0);
	await page.keyboard.press("Tab");
	await expect(
		page.locator(".view-line", { hasText: "second := newValue" }).first(),
	).toBeVisible();
});

test("keeps composer pickers visible in a constrained window", async ({
	page,
}) => {
	await page.setViewportSize({ width: 420, height: 300 });
	await page.route(/\/api\/files\/search/, async (route) => {
		await route.fulfill({
			json: Array.from({ length: 30 }, (_, index) => ({
				path: `src/nested/example-${index}.ts`,
				name: `example-${index}.ts`,
			})),
		});
	});
	await composer(page);
	await page.getByTitle("Add file context").click();

	const picker = page.getByRole("dialog", { name: "Find a file" });
	await expect(picker).toBeVisible();
	await expect(picker.getByPlaceholder("Search files…")).toBeFocused();
	await expectFloatingInViewport(page, picker);
	await page.keyboard.press("Escape");
	await expect(picker).toBeHidden();
});

test("keeps scheduled-agent actions above inspector clipping", async ({
	page,
}) => {
	await mockSavedSessions(page, [
		{
			id: "agent-menu-check",
			title: "Agent menu check",
			updated_at: "2026-08-11T00:00:00Z",
		},
	]);
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				lsp: false,
				tasks: true,
				terminal: true,
			},
		});
	});
	let taskListRequests = 0;
	await page.route(
		/\/api\/v2\/backends\/wingman\/sessions\/[^/]+\/tasks$/,
		async (route) => {
			taskListRequests++;
			await route.fulfill({
				json:
					taskListRequests === 1
						? []
						: [
								{
									id: "running-check",
									description: "Review changes",
									agent_type: "explore",
									status: "running",
									activity: "Reading files",
									elapsed_seconds: 3,
									seq: 1,
								},
							],
			});
		},
	);
	await page.route(
		/\/api\/v2\/backends\/wingman\/sessions\/[^/]+\/schedules$/,
		async (route) => {
			await route.fulfill({
				json: [
					{
						id: "deploy-check",
						prompt: "Check deploy",
						schedule: "every 1h",
						status: "active",
						next_in: "in 42m",
					},
				],
			});
		},
	);
	await composer(page);
	await openSessions(page);
	await page.getByTitle("Agent menu check").click();
	const showAgentsButton = page.getByRole("button", {
		name: "Show background tasks",
		exact: true,
	});
	const sessionsButton = page.getByRole("button", {
		name: /^(Show|Hide) sessions$/,
	});
	await expect(showAgentsButton).toBeVisible({ timeout: 7000 });
	await expect(showAgentsButton).toHaveAttribute("aria-pressed", "false");
	await expect
		.poll(async () => {
			const agentsBox = await showAgentsButton.boundingBox();
			const sessionsBox = await sessionsButton.boundingBox();
			return !!(agentsBox && sessionsBox && agentsBox.x < sessionsBox.x);
		})
		.toBe(true);
	await expect(showAgentsButton.locator("svg.animate-pulse")).toHaveCount(1);
	await showAgentsButton.click();
	await expect(
		page.getByRole("button", { name: "Hide background tasks", exact: true }),
	).toHaveAttribute("aria-pressed", "true");
	const agents = page.locator(
		'[data-chat-auxiliary-panel][data-view="agents"]',
	);
	await expect(agents).toBeVisible();
	await expect(
		page.locator('[data-chat-auxiliary-panel][data-view="sessions"]'),
	).toHaveCount(0);
	await expect
		.poll(async () => {
			const agentsBox = await agents.boundingBox();
			const panelBox = await page
				.locator('[data-layout-panel="center"]')
				.boundingBox();
			return !!(
				agentsBox &&
				panelBox &&
				agentsBox.width <= 240 &&
				agentsBox.width <= panelBox.width / 2 + 1
			);
		})
		.toBe(true);

	await page
		.getByText("Check deploy", { exact: true })
		.click({ button: "right" });

	const menu = page.getByRole("menu", { name: "Agent actions" });
	await expect(menu).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Pause" })).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Delete" })).toBeVisible();
	await expectFloatingInViewport(page, menu);
	await menu.getByRole("menuitem", { name: "Delete" }).click();
	await expect(
		page.getByRole("dialog", { name: "Delete scheduled task?" }),
	).toBeVisible();
});

test("uses one compact left side panel with stable tab order", async ({
	page,
}) => {
	await composer(page);
	const toolbar = page.getByLabel("Window toolbar");
	const sidePanel = page.locator('[data-layout-panel="side"]');
	const frame = page.locator('[data-panel-frame="side"]');
	const sideTabs = page.getByRole("tablist", { name: "Side panel views" });
	const openTabs = page.getByRole("tablist", { name: "Open tabs" });
	const labels = await sideTabs
		.getByRole("tab")
		.evaluateAll((tabs) => tabs.map((tab) => tab.getAttribute("aria-label")));

	await expect(sidePanel).toHaveCount(1);
	await expect(sidePanel).toHaveAttribute("data-panel-side", "left");
	await expect(sidePanel).toHaveCSS("width", "240px");
	await expect(frame).toHaveAttribute("data-panel-side", "left");
	await expect(frame).toHaveCSS("width", "240px");
	await expect(toolbar.locator("[data-titlebar-left-panel]")).toHaveCSS(
		"width",
		"240px",
	);
	expect(labels[0]).toBe("Files");
	expect(labels).not.toContain("Sessions");
	expect(
		await sideTabs.evaluate((element) =>
			element
				.closest("[data-window-titlebar]")
				?.hasAttribute("data-window-titlebar"),
		),
	).toBe(true);

	await expect
		.poll(async () => {
			const panel = await sidePanel.boundingBox();
			const frameBox = await frame.boundingBox();
			const firstTab = await openTabs.getByRole("tab").first().boundingBox();
			if (!panel || !frameBox || !firstTab) return false;
			return (
				Math.abs(panel.x) <= 1 &&
				Math.abs(frameBox.x - panel.x) <= 1 &&
				Math.abs(firstTab.x - (panel.x + panel.width)) <= 1
			);
		})
		.toBe(true);
	await page.reload();
	await expect(page.getByPlaceholder("Message Wingman…")).toBeVisible();
	await expect(sidePanel).toHaveAttribute("data-panel-side", "left");
	expect(
		await sideTabs
			.getByRole("tab")
			.evaluateAll((tabs) => tabs.map((tab) => tab.getAttribute("aria-label"))),
	).toEqual(labels);
});

test("resizes and collapses the unified side panel", async ({ page }) => {
	await composer(page);
	const toolbar = page.getByLabel("Window toolbar");
	const sidePanel = page.locator('[data-layout-panel="side"]');
	const sideContent = page.locator('[data-panel-content="side"]');
	const frame = page.locator('[data-panel-frame="side"]');
	const handle = page.getByRole("separator", { name: "Resize Side Panel" });

	await expect(handle).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
	const handleBox = await handle.boundingBox();
	expect(handleBox).not.toBeNull();
	await page.mouse.move(
		handleBox!.x + handleBox!.width / 2,
		handleBox!.y + handleBox!.height / 2,
	);
	await page.mouse.down();
	await page.mouse.move(handleBox!.x + 120, handleBox!.y + 20, { steps: 5 });
	await expect
		.poll(async () => (await sidePanel.boundingBox())?.width ?? 0)
		.toBeGreaterThan(330);
	await expect
		.poll(async () => {
			const panelBox = await sidePanel.boundingBox();
			const titleBox = await toolbar
				.locator("[data-titlebar-left-panel]")
				.boundingBox();
			return !!(
				panelBox &&
				titleBox &&
				Math.abs(panelBox.width - titleBox.width) < 1
			);
		})
		.toBe(true);
	await page.mouse.move(handleBox!.x + 1_000, handleBox!.y + 20);
	await expect(sidePanel).toHaveCSS("width", "480px");
	await page.mouse.up();
	await expect(frame).toHaveCSS("width", "480px");

	const expandedHandle = await handle.boundingBox();
	expect(expandedHandle).not.toBeNull();
	await page.mouse.move(
		expandedHandle!.x + expandedHandle!.width / 2,
		expandedHandle!.y + expandedHandle!.height / 2,
	);
	await page.mouse.down();
	await page.mouse.move(expandedHandle!.x - 1_000, expandedHandle!.y);
	await page.mouse.up();
	await expect(sidePanel).toHaveCSS("width", "0px");
	await expect(sideContent).toHaveCSS("width", "480px");
	await expect(sideContent).toHaveCSS("opacity", "0");
	await expect(frame).toHaveCSS("opacity", "0");
	await expect(toolbar.locator("[data-titlebar-left-panel]")).toHaveCSS(
		"width",
		"40px",
	);
	await toolbar.getByRole("button", { name: "Show Side Panel" }).click();
	await expect(sidePanel).toHaveCSS("width", "480px");
	await expect(sideContent).toHaveCSS("opacity", "1");
});
test("keeps the desktop workspace mounted across viewport sizes", async ({
	page,
}) => {
	await composer(page);
	const center = page.locator('[data-layout-panel="center"]');
	await center.evaluate((element) => {
		element.setAttribute("data-mount-marker", "original");
	});

	for (const width of [1_000, 700, 1_280]) {
		await page.setViewportSize({ width, height: 800 });
		await expect(center).toHaveAttribute("data-mount-marker", "original");
		await expect(page.locator('[data-layout-panel="side"]')).toHaveCount(1);
		await expect(page.getByRole("dialog", { name: "Sessions" })).toHaveCount(0);
		await expect(page.getByRole("dialog", { name: "Side panel" })).toHaveCount(
			0,
		);
	}
});

test("keeps workspace tabs in the titlebar at small viewport sizes", async ({
	page,
}) => {
	await page.setViewportSize({ width: 700, height: 800 });
	await composer(page);
	const workspaceTabs = page.getByRole("tablist", {
		name: "Side panel views",
	});
	await expect(workspaceTabs).toBeVisible();
	expect(
		await workspaceTabs.evaluate((element) =>
			element
				.closest("[data-window-titlebar]")
				?.hasAttribute("data-window-titlebar"),
		),
	).toBe(true);
});

test("runs a coding tool and renders its result", async ({ page }) => {
	const input = await composer(page);
	await input.fill("create e2e-result.txt");
	await input.press("Enter");

	await page.getByRole("button", { name: "1 tool" }).click();
	const tool = page.getByRole("button", { name: /^Edit file/ });
	await expect(tool).toBeVisible();
	await tool.click();
	await expect(
		page.getByText(/Applied 1 edits across 1 files atomically/),
	).toBeVisible();
	await expect(
		page.getByText("Created the requested file", { exact: true }),
	).toBeVisible();

	const usage = page.getByRole("button", {
		name: /context left|token usage/i,
	});
	await expect(usage).toHaveCount(0);

	await page.getByRole("treeitem", { name: "e2e-result.txt" }).click();
	await expect(usage).toHaveCount(0);
	await page.getByRole("tab", { name: /create e2e-result\.txt/i }).click();
	await expect(usage).toHaveCount(0);
});

test("renders streaming Markdown with lazy Monaco highlighting", async ({
	page,
}) => {
	const input = await composer(page);
	await input.fill("render markdown");
	await input.press("Enter");

	const markdown = page.locator("[data-markdown-content]").last();
	await expect(
		markdown.getByRole("heading", { name: "Migration result" }),
	).toBeVisible();
	await expect(markdown.getByRole("checkbox", { name: "" })).toBeChecked();
	await expect(markdown.getByRole("cell", { name: "ready" })).toBeVisible();

	const goBlock = markdown.locator('[data-markdown-code][data-language="go"]');
	await expect(goBlock).toContainText("package main");
	await expect(goBlock.locator(".md-token-keyword").first()).toHaveText(
		"package",
	);
	await expect(
		goBlock.getByRole("button", { name: "Copy code" }),
	).toBeVisible();

	const mermaidBlock = markdown
		.locator('[data-markdown-code][data-language="mermaid"]')
		.first();
	await expect(mermaidBlock.locator("[data-mermaid-preview]")).toBeVisible();
	await mermaidBlock
		.getByRole("button", { name: "Show Mermaid source" })
		.click();
	await expect(mermaidBlock.locator("pre > code")).toBeVisible();
	await expect(mermaidBlock).toContainText("graph TD; A-->B");

	const c4Block = markdown
		.locator('[data-markdown-code][data-language="mermaid"]')
		.nth(1);
	const c4Image = c4Block.locator("[data-mermaid-preview] img");
	await expect(c4Image).toBeVisible();
	await expect
		.poll(() =>
			c4Image.evaluate((image: HTMLImageElement) => image.naturalWidth),
		)
		.toBeGreaterThan(0);
	await expect(markdown).toContainText("Math stays literal: $x^2$.");
	await expect(
		markdown.getByRole("link", { name: "Documentation" }),
	).toHaveAttribute("target", "_blank");
});

test("renders an elicitation form and returns the chosen option", async ({
	page,
}) => {
	const input = await composer(page);
	await input.fill("pick a color");
	await input.press("Enter");

	await expect(page.getByText("Which color?").first()).toBeVisible();
	const azure = page.getByRole("button", { name: /Azure/ });
	await expect(azure).toBeVisible();
	await expect(page.getByText("cool")).toBeVisible();
	await azure.click();

	await expect(
		page.getByText("You chose: Azure", { exact: true }),
	).toBeVisible();
});

test("cancels an active coding turn", async ({ page, request }) => {
	const input = await composer(page);
	await input.fill("cancel this request");
	await input.press("Enter");
	await expect(
		page.getByText("Long-running work", { exact: true }),
	).toBeVisible();

	await page.getByTitle("Stop (Esc)").click();
	await expect(page.getByText("Cancelled", { exact: true })).toBeVisible();
	const cancelled = await request.get(`${controlURL()}/cancelled`);
	expect(cancelled.ok()).toBeTruthy();
});

test("steers an active turn without merging response boundaries", async ({
	page,
	request,
}) => {
	const input = await composer(page);
	await input.fill("initial request");
	await input.press("Enter");
	await expect(page.getByText("Working", { exact: true })).toBeVisible();

	await input.fill("steer this turn");
	await input.press("Enter");
	await expect(input).toHaveValue("");
	await expect(
		page.getByText("steer this turn", { exact: true }),
	).toBeVisible();

	const release = await request.post(`${controlURL()}/release-steer`);
	expect(release.ok()).toBeTruthy();
	await expect(
		page.getByText("Steering applied", { exact: true }),
	).toBeVisible();
	await expect(
		page.getByText("WorkingSteering applied", { exact: true }),
	).toHaveCount(0);
	await expect(page.getByText("Queue paused", { exact: true })).toHaveCount(0);
});

test("runs a command in a terminal tab", async ({ page }) => {
	await composer(page);

	await openNewTerminal(page);

	const screen = page.locator(".xterm-screen");
	await expect(screen).toBeVisible();
	await screen.click();

	await page.keyboard.type('echo term""-e2e-ok');
	await page.keyboard.press("Enter");

	await expect(page.locator(".xterm-rows").first()).toContainText(
		"term-e2e-ok",
		{ timeout: 15_000 },
	);

	await page.keyboard.type("printf '\\033]0;e2e-title\\007'; sleep 3");
	await page.keyboard.press("Enter");
	const terminalTab = page.getByRole("tab", { name: /^e2e-title\./ });
	await expect(terminalTab).toBeVisible({ timeout: 15_000 });

	await expect(page.getByTitle("Hide terminal", { exact: true })).toHaveCount(
		0,
	);

	await screen.click();
	await page.keyboard.type("echo terminal-close-$((20+22))");
	await page.keyboard.press("Enter");
	await expect(page.locator(".xterm-rows").first()).toContainText(
		"terminal-close-42",
	);
	await terminalTab.press("Delete");
	await expect(terminalTab).toBeHidden();
	await expect(
		page.getByRole("dialog", { name: "Terminate terminal?" }),
	).toHaveCount(0);

	const remainingTerminals = page.locator('[data-center-tab^="terminal:"]');
	for (
		let remaining = await remainingTerminals.count();
		remaining > 0;
		remaining--
	) {
		await remainingTerminals.first().click();
		await remainingTerminals.first().press("Delete");
		await expect(remainingTerminals).toHaveCount(remaining - 1);
	}
});

test("docks only terminals at the bottom with tabs on the right", async ({
	page,
	request,
}) => {
	await composer(page);
	await page.keyboard.press("Control+k");
	await expect(
		page.getByRole("option", {
			name: "Show Terminal at Bottom",
			exact: true,
		}),
	).toBeVisible();
	await page.keyboard.press("Escape");
	await openNewTerminal(page);
	await expect(page.locator(".xterm-screen")).toBeVisible();
	const originalSurface = page.locator("[data-terminal-surface]:not(.hidden)");
	const originalScreen = originalSurface.locator(".xterm-screen");
	await originalScreen.evaluate((element) => {
		element.setAttribute("data-mount-marker", "original");
	});

	await page.keyboard.press("Control+k");
	await page
		.getByRole("option", { name: "Show Terminal at Bottom", exact: true })
		.click();
	const dock = page.locator('[data-layout-panel="terminal"]');
	const terminalTabs = page.getByRole("tablist", { name: "Terminal tabs" });
	await expect(dock).toBeVisible();
	await expect(
		page
			.getByRole("tablist", { name: "Open tabs" })
			.locator('[data-center-tab^="terminal:"]'),
	).toHaveCount(0);
	await expect(terminalTabs.getByRole("tab")).toHaveCount(1);
	await expect(originalScreen).toHaveAttribute("data-mount-marker", "original");
	await expect(page.locator("[data-terminal-tabs-separator]")).toHaveCSS(
		"width",
		"1px",
	);
	await expect(page.locator("[data-terminal-dock-separator]")).toHaveCSS(
		"height",
		"1px",
	);
	await expect(
		page.getByRole("button", { name: /Move terminals to/ }),
	).toHaveCount(0);
	const center = page.locator('[data-layout-panel="center"]');
	const chatComposer = page.locator("[data-chat-composer]");
	const wingmanLogo = page.getByRole("img", { name: "Wingman" });
	await expect(chatComposer).toBeVisible();
	await expect
		.poll(async () => {
			const dockBox = await dock.boundingBox();
			const composerBox = await chatComposer.boundingBox();
			if (!dockBox || !composerBox) return 999;
			return dockBox.y - (composerBox.y + composerBox.height);
		})
		.toBeLessThan(80);
	await expect
		.poll(async () => {
			const centerBox = await center.boundingBox();
			const dockBox = await dock.boundingBox();
			const logoBox = await wingmanLogo.boundingBox();
			if (!centerBox || !dockBox || !logoBox) return 999;
			const contentMiddle = centerBox.y + (dockBox.y - centerBox.y) / 2;
			return Math.abs(logoBox.y + logoBox.height / 2 - contentMiddle);
		})
		.toBeLessThan(100);

	await page
		.locator('[aria-label="Terminal dock"]')
		.getByRole("button", { name: "New terminal", exact: true })
		.click();
	await expect(terminalTabs.getByRole("tab")).toHaveCount(2);
	await expect(
		page.getByRole("button", { name: "Close active terminal" }),
	).toHaveCount(0);
	await terminalTabs.getByRole("tab").first().click({ button: "right" });
	const terminalMenu = page.getByRole("menu", { name: "Terminal actions" });
	await expect(
		terminalMenu.getByRole("menuitem", { name: "Close", exact: true }),
	).toBeVisible();
	await expect(
		terminalMenu.getByRole("menuitem", { name: "Close Others" }),
	).toBeEnabled();
	await expect(
		terminalMenu.getByRole("menuitem", { name: "Close All" }),
	).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(
		page.locator("[data-terminal-surface]:not(.hidden) .xterm-screen"),
	).toHaveCount(1);
	await expect
		.poll(async () => {
			const screen = await page
				.locator("[data-terminal-surface]:not(.hidden) .xterm-screen")
				.boundingBox();
			const list = await terminalTabs.boundingBox();
			return !!(screen && list && list.x >= screen.x + screen.width - 1);
		})
		.toBe(true);
	const bottomCapabilities = await request.get("/api/capabilities");
	expect(
		((await bottomCapabilities.json()) as Record<string, unknown>)[
			"window.terminal.position"
		],
	).toBe("bottom");

	await page.keyboard.press("Control+k");
	await page
		.getByRole("option", { name: "Show Terminal in Tab", exact: true })
		.click();
	await expect(dock).toHaveCount(0);
	const tabCapabilities = await request.get("/api/capabilities");
	expect(
		((await tabCapabilities.json()) as Record<string, unknown>)[
			"window.terminal.position"
		],
	).toBe("tab");
	const centerTerminals = page
		.getByRole("tablist", { name: "Open tabs" })
		.locator('[data-center-tab^="terminal:"]');
	await expect(centerTerminals).toHaveCount(2);
	for (let remaining = 2; remaining > 0; remaining -= 1) {
		await centerTerminals.first().press("Delete");
		await expect(centerTerminals).toHaveCount(remaining - 1);
	}
});

test("follows the system theme in browser previews", async ({ page }) => {
	await page.emulateMedia({ colorScheme: "dark" });
	await composer(page);
	await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
	await expect(page.locator("link[data-wingman-favicon]")).toHaveAttribute(
		"href",
		"/icon_dark.svg",
	);

	await page.getByRole("treeitem", { name: /theme-preview\.html/ }).click();
	await page.getByTitle("Show browser preview").click();
	const preview = page.getByTitle("Preview of theme-preview.html");
	await expect(preview).toBeVisible();
	await expect(preview).toHaveCSS("color-scheme", "dark");

	await page.emulateMedia({ colorScheme: "light" });
	await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
	await expect(preview).toHaveCSS("color-scheme", "light");
	await page.reload();
	await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
});

test("renders markdown files in a browser preview", async ({ page }) => {
	await composer(page);
	await page.getByRole("treeitem", { name: /readme-preview\.md/ }).click();
	await page.getByTitle("Show Markdown preview").click();

	const preview = page.getByRole("article", {
		name: "Preview of readme-preview.md",
	});
	await expect(preview).toBeVisible();
	await expect(
		preview.getByRole("heading", { name: "Markdown preview" }),
	).toBeVisible();
	await expect(preview.getByText("Rendered in the browser.")).toBeVisible();

	await page.getByTitle("Show code editor").click();
	await expect(page.locator(".monaco-editor")).toBeVisible();
});

test("uses Wingman's dynamic editor context menu", async ({
	page,
	request,
}) => {
	await resetCompletionFile(request);
	await page.setViewportSize({ width: 640, height: 400 });
	let definitionRequested = false;
	await page.route(/\/api\/lsp\/definition$/, async (route) => {
		definitionRequested = true;
		await route.fulfill({ json: [] });
	});
	await composer(page);
	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	const editor = page.locator(".monaco-editor");
	await editor.click();

	for (const shortcut of ["F1", "Control+Shift+P"]) {
		await page.keyboard.press(shortcut);
		await expect(page.locator(".quick-input-widget")).not.toBeVisible();
		const wingmanPalette = page.getByRole("dialog", {
			name: "Command palette",
		});
		if (await wingmanPalette.isVisible()) await page.keyboard.press("Escape");
	}

	await editor.click({ button: "right" });
	const menu = page.getByRole("menu", { name: "Editor actions" });
	await expect(menu).toBeVisible();
	for (const item of ["Undo", "Redo", "Cut", "Copy", "Paste"]) {
		await expect(
			menu.getByRole("menuitem", { name: item, exact: true }),
		).toBeVisible();
	}
	for (const item of ["Toggle Line Comment", "Toggle Block Comment"]) {
		const action = menu.getByRole("menuitem", { name: item, exact: true });
		await expect(action).toBeVisible();
		await expect(action).toBeEnabled();
	}
	for (const item of [
		"Go to Definition",
		"Go to Implementations",
		"Find All References",
	]) {
		const action = menu.getByRole("menuitem", { name: item, exact: true });
		await expect(action).toBeVisible();
		await expect(action).toBeEnabled();
	}
	await expect(
		menu.getByRole("menuitem", { name: "Go to Type Definition" }),
	).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Change All Occurrences" }),
	).toBeVisible();
	for (const item of [
		"Peek Definition",
		"Peek Type Definition",
		"Peek Implementations",
	]) {
		await expect(
			menu.getByRole("menuitem", { name: item, exact: true }),
		).toHaveCount(0);
	}

	const editorMenuBox = await menu.boundingBox();
	await page.keyboard.down("Alt");
	for (const item of [
		"Peek Definition",
		"Peek Type Definition",
		"Peek Implementations",
		"Find All References",
	]) {
		const action = menu.getByRole("menuitem", { name: item, exact: true });
		await expect(action).toBeVisible();
		await expect(action).toBeEnabled();
	}
	await expect(
		menu.getByRole("menuitem", { name: "Change All Occurrences" }),
	).toBeVisible();
	for (const item of [
		"Go to Definition",
		"Go to Type Definition",
		"Go to Implementations",
	]) {
		await expect(
			menu.getByRole("menuitem", { name: item, exact: true }),
		).toHaveCount(0);
	}
	await expect.poll(() => menu.boundingBox()).toEqual(editorMenuBox);
	await page.keyboard.up("Alt");
	await expect(
		menu.getByRole("menuitem", { name: "Go to Definition", exact: true }),
	).toBeVisible();
	await expect(
		menu.getByRole("menuitem", { name: "Peek Definition", exact: true }),
	).toHaveCount(0);
	await expect(
		page.getByRole("menuitem", { name: "Command Palette", exact: true }),
	).toHaveCount(0);
	await expectFloatingInViewport(page, menu, 4);
	await expectFloatingNotScrollable(menu);

	await menu
		.getByRole("menuitem", { name: "Go to Definition", exact: true })
		.click();
	await expect.poll(() => definitionRequested).toBe(true);
	const viewLines = editor.locator(".view-lines");
	const packageLine = editor
		.locator(".view-line")
		.filter({ hasText: "package main" })
		.first();
	await packageLine.click();
	await page.keyboard.press("Home");
	await page.keyboard.press("Shift+F10");
	await expect(menu).toBeVisible();

	await menu
		.getByRole("menuitem", { name: "Toggle Line Comment", exact: true })
		.click();
	await expect(viewLines).toContainText("// package main");

	await packageLine.click();
	await page.keyboard.press("Home");
	await page.keyboard.press("Shift+F10");
	await menu
		.getByRole("menuitem", { name: "Toggle Line Comment", exact: true })
		.click();
	await expect(viewLines).not.toContainText("// package main");
	await expect(viewLines).toContainText("package main");

	await editor.focus();
	await page.keyboard.press("ControlOrMeta+A");
	await page.keyboard.press("Shift+F10");
	await menu
		.getByRole("menuitem", { name: "Toggle Block Comment", exact: true })
		.click();
	await expect(viewLines).toContainText(/\/\*\s*package\s+main/);
	await expect(viewLines).toContainText(/\*\//);

	await editor.focus();
	await page.keyboard.press("Shift+F10");
	await menu
		.getByRole("menuitem", { name: "Toggle Block Comment", exact: true })
		.click();
	await expect(viewLines).not.toContainText(/\/\*|\*\//);
	await expect(viewLines).toContainText("package main");
});

test("previews selection transformations and can hand the selection to chat", async ({
	page,
	request,
}) => {
	const requests: Array<{
		path: string;
		content: string;
		instruction: string;
		version: number;
		range: {
			start_line: number;
			start_column: number;
			end_line: number;
			end_column: number;
		};
	}> = [];
	await page.route(/\/api\/editor\/transform$/, async (route) => {
		const body = route.request().postDataJSON() as (typeof requests)[number];
		requests.push(body);
		await route.fulfill({
			json: {
				version: body.version,
				edit: {
					range: body.range,
					expected_text: "original",
					replacement: "transformed",
				},
			},
		});
	});

	await composer(page);
	await page.getByRole("treeitem", { name: /editable\.txt/ }).click();
	const editor = page.locator(".monaco-editor").first();
	const original = editor
		.locator(".view-line", { hasText: "original" })
		.first();
	await expect(original).toBeVisible();
	const selectOriginal = async () => {
		await original.click();
		await page.keyboard.press("Home");
		await page.keyboard.press("Shift+End");
	};

	await selectOriginal();
	await page.keyboard.press("ControlOrMeta+K");
	let palette = page.getByRole("dialog", { name: "Command palette" });
	await expect(palette).toBeVisible();
	let paletteActions = await palette.getByRole("option").allTextContents();
	expect(paletteActions.indexOf("Chat about this…Selected text")).toBeLessThan(
		paletteActions.indexOf("Transform selection…Selected text"),
	);
	await palette
		.getByRole("option", { name: "Transform selection… Selected text" })
		.click();
	const prompt = page.getByRole("dialog", { name: "Transform selection" });
	await expect(prompt).toBeVisible();
	await prompt.getByRole("button", { name: "Refactor", exact: true }).click();
	await expect(
		page
			.locator(".monaco-editor:visible")
			.filter({ hasText: "transformed" })
			.first(),
	).toBeVisible();
	expect(requests).toHaveLength(1);
	expect(requests[0].path).toBe("editable.txt");
	expect(requests[0].content).toBe("original\n");
	expect(requests[0].instruction).toContain("Refactor this selection");
	await page.keyboard.press("Escape");
	await expect(original).toBeVisible();

	await selectOriginal();
	await page.keyboard.press("ControlOrMeta+K");
	palette = page.getByRole("dialog", { name: "Command palette" });
	await palette
		.getByRole("option", { name: "Transform selection… Selected text" })
		.click();
	await prompt
		.getByRole("textbox", { name: "Transformation instruction" })
		.fill("Make it clearer");
	await prompt.getByRole("button", { name: "Generate", exact: true }).click();
	await expect(
		page
			.locator(".monaco-editor:visible")
			.filter({ hasText: "transformed" })
			.first(),
	).toBeVisible();
	await page.keyboard.press("Tab");
	const transformed = page
		.locator(".view-line:visible", { hasText: "transformed" })
		.first();
	// Monaco may use the first Tab to focus its range-edit preview and the
	// second to accept, depending on available editor space.
	if (!(await transformed.isVisible())) await page.keyboard.press("Tab");
	await expect(transformed).toBeVisible();
	await expect
		.poll(() => page.locator(".monaco-editor:visible").count())
		.toBe(1);
	const disk = await request.get("/api/files/read?path=editable.txt");
	expect(((await disk.json()) as { content: string }).content).toBe(
		"original\n",
	);
	await page.keyboard.press("ControlOrMeta+Z");
	await expect(original).toBeVisible();

	await selectOriginal();
	await page.keyboard.press("Shift+F10");
	const menu = page.getByRole("menu", { name: "Editor actions" });
	const menuLabels = await menu.getByRole("menuitem").allTextContents();
	expect(menuLabels.indexOf("Chat about this…")).toBeLessThan(
		menuLabels.findIndex((label) => label.startsWith("Transform Selection…")),
	);
	await page.keyboard.press("Escape");
	await page.keyboard.press("ControlOrMeta+K");
	palette = page.getByRole("dialog", { name: "Command palette" });
	paletteActions = await palette.getByRole("option").allTextContents();
	expect(paletteActions).toContain("Chat about this…Selected text");
	await palette
		.getByRole("option", { name: "Chat about this… Selected text" })
		.click();
	const chatInput = page.getByPlaceholder("Message Wingman…");
	await expect(chatInput).toBeVisible();
	await expect(chatInput).toHaveValue(
		/Help me with this selection from editable\.txt:1/,
	);
	await expect(chatInput).toHaveValue(/original/);
	await expect(
		page.locator("[data-chat-composer]").getByText("editable.txt", {
			exact: true,
		}),
	).toBeVisible();
});

test("routes native File menu commands through shared file handling", async ({
	page,
	request,
}) => {
	await page.addInitScript(() => {
		const states: Array<{ command: string; enabled: boolean }> = [];
		Reflect.set(window, "__shellCommandStates", states);
		window.addEventListener("shell:command-state", (event) => {
			states.push(
				(event as CustomEvent<{ command: string; enabled: boolean }>).detail,
			);
		});
	});
	await composer(page);
	const commandEnabled = (command: string) =>
		page.evaluate((name) => {
			const states = Reflect.get(window, "__shellCommandStates") as Array<{
				command: string;
				enabled: boolean;
			}>;
			return states.findLast((state) => state.command === name)?.enabled;
		}, command);
	await expect.poll(() => commandEnabled("save")).toBe(false);
	await expect.poll(() => commandEnabled("save-as")).toBe(false);

	await page.evaluate(() =>
		window.dispatchEvent(
			new CustomEvent("shell:command", { detail: "new-file" }),
		),
	);
	await expect(page.getByRole("dialog", { name: "New File" })).toHaveCount(0);
	await expect(page.getByRole("tab", { name: /Untitled/ })).toBeVisible();
	await expect.poll(() => commandEnabled("save")).toBe(true);
	await expect.poll(() => commandEnabled("save-as")).toBe(true);
	const editor = page.locator(".monaco-editor");
	await editor.click();
	await page.keyboard.type("created through the File menu\n");

	await page.evaluate(() =>
		window.dispatchEvent(new CustomEvent("shell:command", { detail: "save" })),
	);
	const saveAs = page.getByRole("dialog", { name: "Save As" });
	await expect(saveAs).toBeVisible();
	await saveAs
		.getByRole("textbox", { name: "File path" })
		.pressSequentially("menu-created.txt");
	await saveAs.getByRole("button", { name: "Save" }).click();
	await expect(
		page.getByRole("tab", { name: "menu-created.txt" }),
	).toBeVisible();

	await page.evaluate(() =>
		window.dispatchEvent(
			new CustomEvent("shell:command", { detail: "save-as" }),
		),
	);
	await expect(saveAs).toBeVisible();
	await saveAs
		.getByRole("textbox", { name: "File path" })
		.fill("menu-copy.txt");
	await saveAs.getByRole("button", { name: "Save" }).click();

	await expect(page.getByRole("tab", { name: "menu-copy.txt" })).toBeVisible();
	let read = await request.get("/api/files/read?path=menu-copy.txt");
	expect(read.ok()).toBeTruthy();
	let file = (await read.json()) as { content: string };
	expect(file.content).toContain("created through the File menu");

	await editor.click();
	await page.keyboard.type("saved in place\n");
	await page.evaluate(() =>
		window.dispatchEvent(new CustomEvent("shell:command", { detail: "save" })),
	);
	await expect
		.poll(async () => {
			read = await request.get("/api/files/read?path=menu-copy.txt");
			file = (await read.json()) as { content: string };
			return file.content;
		})
		.toContain("saved in place");
});

test("previews raster images at their intrinsic size and renders SVG", async ({
	page,
}) => {
	await composer(page);
	await page.getByRole("treeitem", { name: /pixel\.png/ }).click();
	const pixel = page.getByRole("img", { name: "pixel.png" });
	await expect(pixel).toBeVisible();
	await expect
		.poll(() => pixel.evaluate((image) => image.naturalWidth))
		.toBe(1);
	await expect.poll(() => pixel.evaluate((image) => image.clientWidth)).toBe(1);

	await page.getByRole("treeitem", { name: /logo-preview\.svg/ }).click();
	const svg = page.getByRole("img", { name: "logo-preview.svg" });
	await expect(svg).toBeVisible();
	await expect.poll(() => svg.evaluate((image) => image.naturalWidth)).toBe(40);
	await page.getByTitle("Show code editor").click();
	await expect(page.locator(".monaco-editor")).toBeVisible();
});

test("previews structured data and delimited tables", async ({ page }) => {
	await composer(page);

	for (const filename of [
		"data-preview.json",
		"data-preview.yaml",
		"data-preview.toml",
		"data-preview.xml",
	]) {
		await page.getByRole("treeitem", { name: filename }).click();
		await page.getByTitle("Show data preview").click();
		const preview = page.getByLabel(`Preview of ${filename}`);
		await expect(preview).toBeVisible();
		await expect(preview.getByText("project:")).toBeVisible();
		await expect(preview.getByText('"wingman"')).toBeVisible();
	}

	await page.getByRole("treeitem", { name: "data-preview.json" }).click();
	const structured = page.getByLabel("Preview of data-preview.json");
	await structured.getByRole("button", { name: "Graph" }).click();
	await expect(structured.getByText('"wingman"')).toBeVisible();
	await expect(structured.getByText('"preview"')).toBeVisible();
	await structured.getByTitle("Collapse features").click();
	await expect(structured.getByText('"preview"')).not.toBeVisible();
	await structured.getByTitle("Expand features").click();
	await expect(structured.getByText('"preview"')).toBeVisible();
	await structured.getByRole("button", { name: "Tree" }).click();
	await expect(structured.getByText("project:")).toBeVisible();

	for (const filename of ["data-preview.csv", "data-preview.tsv"]) {
		await page.getByRole("treeitem", { name: filename }).click();
		const preview = page.getByLabel(`Preview of ${filename}`);
		await expect(
			preview.getByRole("columnheader", { name: "name" }),
		).toBeVisible();
		await expect(
			preview.getByRole("cell", { name: "ready" }).first(),
		).toBeVisible();
		await preview.getByRole("button", { name: "name" }).click();
		await expect(
			preview.getByRole("columnheader", { name: "name" }),
		).toHaveAttribute("aria-sort", "ascending");
		await page.getByTitle("Show code editor").click();
		await expect(page.locator(".monaco-editor")).toBeVisible();
	}
});

test("renders Mermaid diagrams", async ({ page }) => {
	await composer(page);
	await page.getByRole("treeitem", { name: /flow-preview\.mmd/ }).click();
	const preview = page.getByRole("img", {
		name: "Preview of flow-preview.mmd",
	});
	await expect(preview).toBeVisible();
	await expect
		.poll(() => preview.evaluate((image) => image.naturalWidth))
		.toBeGreaterThan(0);
	await page.getByTitle("Show code editor").click();
	await expect(page.locator(".monaco-editor")).toBeVisible();
});

test("protects unsaved file edits when closing a tab", async ({
	page,
	request,
}) => {
	await composer(page);
	await page.getByRole("treeitem", { name: /editable\.txt/ }).click();
	const editor = page.locator(".monaco-editor");
	await expect(editor).toBeVisible();
	await editor.click();
	await page.keyboard.press("Control+End");
	await page.keyboard.type(" edited");

	const editableTab = page.getByRole("tab", {
		name: /editable\.txt, unsaved changes/,
	});
	await expect(editableTab).toBeVisible();
	await editableTab.hover();
	await page.getByTitle("Close editable.txt", { exact: true }).click();
	const closeDialog = page.getByRole("dialog", {
		name: "Save changes before closing?",
	});
	await expect(closeDialog).toBeVisible();
	await expect(closeDialog.locator("..")).toHaveCSS("backdrop-filter", "none");
	await closeDialog.getByRole("button", { name: "Cancel" }).click();
	await expect(editor).toBeVisible();

	await editableTab.hover();
	await page.getByTitle("Close editable.txt", { exact: true }).click();
	await closeDialog.getByRole("button", { name: "Discard" }).click();
	await expect(page.getByRole("tab", { name: /editable\.txt/ })).toHaveCount(0);

	const response = await request.get("/api/files/read?path=editable.txt");
	expect(response.ok()).toBeTruthy();
	expect((await response.json()).content).toBe("original\n");
});

test("uses accessible desktop panels and command navigation", async ({
	page,
}) => {
	await page.setViewportSize({ width: 720, height: 800 });
	await composer(page);

	await expect(page.locator('[data-layout-panel="side"]')).toHaveCount(1);
	await expect(page.getByRole("dialog", { name: "Sessions" })).toHaveCount(0);
	await expect(page.getByRole("dialog", { name: "Side panel" })).toHaveCount(0);

	await page.keyboard.press("Control+k");
	await expect(
		page.getByRole("dialog", { name: "Command palette" }),
	).toBeVisible();
	const palette = page.getByRole("dialog", { name: "Command palette" });
	await expect(
		palette.getByRole("combobox", { name: "Search commands" }),
	).toBeFocused();
	await expect(
		palette.getByText("Show sessions", { exact: true }),
	).toBeVisible();
	await expect(
		palette.getByText("Hide Side Panel", { exact: true }),
	).toBeVisible();
	await expect(palette.getByText(/Move Side Panel/)).toHaveCount(0);
	await expect(
		palette.getByText(/^New Terminal(?: \(.+\))?$/).first(),
	).toBeVisible();
	await page.keyboard.press("Escape");

	const results = await new AxeBuilder({ page })
		.withTags(["wcag2a", "wcag2aa"])
		.analyze();
	expect(
		results.violations.filter(
			(violation) =>
				violation.impact === "serious" || violation.impact === "critical",
		),
	).toEqual([]);
});

test("asks before installing the debugger and shows popup state and retry", async ({
	page,
}) => {
	await writeFile(
		workspacePath("completion.go"),
		"package main\n\nfunc main() {}\n",
	);
	await page.route(/\/api\/capabilities$/, async (route) => {
		const response = await route.fetch();
		await route.fulfill({
			json: { ...(await response.json()), debug: false, tab: false },
		});
	});
	await page.addInitScript(() => {
		const originalFetch = window.fetch.bind(window);
		const state = window as typeof window & { debugInstallRequests: number };
		state.debugInstallRequests = 0;
		let installedPlan: unknown;
		const tools = [
			{
				tool: "delve",
				label: "Go debugger",
				installed: false,
				installable: true,
			},
		];
		window.fetch = (input, init) => {
			if (input !== "/api/debug/plan") return originalFetch(input, init);
			const install = JSON.parse(init?.body as string).install;
			if (!install && !installedPlan) {
				return Promise.resolve(
					new Response(
						JSON.stringify({ type: "installation_required", tools }) + "\n",
					),
				);
			}
			if (install) state.debugInstallRequests++;
			if (install && state.debugInstallRequests === 1) {
				return Promise.resolve(
					new Response(
						JSON.stringify({
							type: "error",
							error:
								"Debugger setup is already in progress. Try again when it finishes.",
						}) + "\n",
					),
				);
			}
			const encoder = new TextEncoder();
			return Promise.resolve(
				new Response(
					new ReadableStream({
						start(controller) {
							if (installedPlan)
								controller.enqueue(
									encoder.encode(
										JSON.stringify({
											type: "tools",
											tools: tools.map((tool) => ({
												...tool,
												installed: true,
											})),
										}) + "\n",
									),
								);
							controller.enqueue(
								encoder.encode(
									JSON.stringify({
										type: "progress",
										progress: {
											tool: "delve",
											label: "Go debugger",
											phase: installedPlan ? "updating" : "installing",
											current: 1,
											total: 1,
										},
									}) + "\n",
								),
							);
							window.addEventListener(
								"finish-debug-setup",
								(event) => {
									let result = (event as CustomEvent).detail;
									if (result.type === "update-failed")
										result = {
											type: "plan",
											plan: installedPlan,
											warning:
												"Could not update debugger tools. Using the installed version.",
										};
									if (result.type === "plan") {
										installedPlan = result.plan;
										controller.enqueue(
											encoder.encode(
												JSON.stringify({
													type: "tools",
													tools: tools.map((tool) => ({
														...tool,
														installed: true,
													})),
												}) + "\n",
											),
										);
									}
									controller.enqueue(
										encoder.encode(JSON.stringify(result) + "\n"),
									);
									controller.close();
								},
								{ once: true },
							);
						},
					}),
					{ headers: { "Content-Type": "application/x-ndjson" } },
				),
			);
		};
	});
	await composer(page);
	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	await page
		.locator(".codelens-decoration")
		.getByText("Debug", { exact: true })
		.click();
	const popup = page.getByRole("dialog", { name: "Debug main" });
	await expect(popup.getByText("Not installed", { exact: true })).toBeVisible();
	await expect(
		popup.getByRole("button", { name: "Install debugger" }),
	).toBeVisible();
	const installRequests = () =>
		page.evaluate(
			() =>
				(window as typeof window & { debugInstallRequests: number })
					.debugInstallRequests,
		);
	expect(await installRequests()).toBe(0);
	await popup.getByRole("button", { name: "Cancel" }).click();
	await expect(popup).toHaveCount(0);
	await page
		.locator(".codelens-decoration")
		.getByText("Debug", { exact: true })
		.click();
	await expect(popup.getByText("Not installed", { exact: true })).toBeVisible();
	expect(await installRequests()).toBe(0);
	await popup.getByRole("button", { name: "Install debugger" }).click();
	await expect(
		popup.getByText(
			"Debugger setup is already in progress. Try again when it finishes.",
		),
	).toBeVisible();
	await expect(popup.getByText("Not installed", { exact: true })).toBeVisible();
	await expect(
		popup.getByText("Installation failed", { exact: true }),
	).toHaveCount(0);
	await popup.getByRole("button", { name: "Retry" }).click();
	await expect(popup.getByRole("status")).toContainText(
		"Installing Go debugger…",
	);
	await expect(
		popup.getByRole("button", { name: "Start debugging" }),
	).toHaveCount(0);
	await page.evaluate(() =>
		window.dispatchEvent(
			new CustomEvent("finish-debug-setup", {
				detail: { type: "error", error: "Debugger download failed" },
			}),
		),
	);
	await expect(popup.getByText("Debugger download failed")).toBeVisible();
	await expect(
		popup.getByText("Installation failed", { exact: true }),
	).toBeVisible();
	await popup.getByRole("button", { name: "Retry" }).click();
	await expect(popup.getByRole("status")).toContainText(
		"Installing Go debugger…",
	);
	await page.evaluate(() =>
		window.dispatchEvent(
			new CustomEvent("finish-debug-setup", {
				detail: {
					type: "plan",
					plan: {
						action: "debug",
						title: "Debug main",
						adapter: "delve",
						terminal_available: true,
						project_dir: ".",
						request: "launch",
						io: "output",
						configuration: { program: "." },
						breakpoints: [],
						function_breakpoints: [],
					},
				},
			}),
		),
	);
	await expect(
		popup.getByRole("button", { name: "Start debugging" }),
	).toBeVisible();
	await expect(popup.getByRole("status")).toHaveCount(0);
	await expect(popup.getByText("Installed", { exact: true })).toBeVisible();
	expect(await installRequests()).toBe(3);
	await popup.getByRole("button", { name: "Cancel" }).click();
	await expect(popup).toHaveCount(0);
	await page
		.locator(".codelens-decoration")
		.getByText("Debug", { exact: true })
		.click();
	await expect(popup.getByRole("status")).toContainText(
		"Updating Go debugger…",
	);
	await expect(popup.getByText("Updating…", { exact: true })).toBeVisible();
	await page.evaluate(() =>
		window.dispatchEvent(
			new CustomEvent("finish-debug-setup", {
				detail: { type: "update-failed" },
			}),
		),
	);
	await expect(popup.getByText("Installed", { exact: true })).toBeVisible();
	await expect(
		popup.getByText(
			"Could not update debugger tools. Using the installed version.",
		),
	).toBeVisible();
	await expect(
		popup.getByRole("button", { name: "Start debugging" }),
	).toBeVisible();
	await expect(
		popup.getByRole("button", { name: "Install debugger" }),
	).toHaveCount(0);
	expect(await installRequests()).toBe(3);
});

test("stops the active debugger when the Debug tab closes", async ({
	page,
}) => {
	let session = {
		session_id: "debug-session-1",
		adapter: "delve",
		language: "Go",
		target: "./cmd/example",
		mode: "debug",
		request: "launch",
		io: "terminal",
		terminal_id: "debug-terminal-1",
		capabilities: { supports_step_back: false },
		state_version: 3,
		state: "running",
		started_at: "2026-08-19T10:00:00Z",
	};
	const controls: Array<Record<string, unknown>> = [];

	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				git_init: false,
				lsp: false,
				debug: true,
				tasks: false,
				terminal: true,
				platform: "linux",
				workspace_name: "workspace",
			},
		});
	});
	await page.route(/\/api\/debug\/inspection(?:\?[^#]*)?$/, async (route) => {
		await route.fulfill({
			json: { session, output: "ready\n", threads: [], frames: [] },
		});
	});
	await page.route(/\/api\/debug\/session$/, async (route) => {
		await route.fulfill({ json: { session } });
	});
	await page.route(/\/api\/debug\/state(?:\?[^#]*)?$/, async (route) => {
		await route.fulfill({
			json: { available: true, session, breakpoints: [] },
		});
	});
	await page.route(/\/api\/debug\/control$/, async (route) => {
		controls.push(route.request().postDataJSON());
		session = { ...session, state: "terminated", state_version: 4 };
		await route.fulfill({
			json: { session },
		});
	});

	await composer(page);
	const debugTab = page.locator('[data-center-tab="debug"]');
	await expect(debugTab).toBeVisible();
	await debugTab.click();
	await expect(
		page.locator('[data-center-tab="terminal:debug-terminal-1"]'),
	).toHaveCount(0);
	const details = page.getByLabel("Debugger details", { exact: true });
	await expect(details).toBeVisible();
	const initialWidth = (await details.boundingBox())?.width ?? 0;
	const resizeHandle = page.getByRole("separator", {
		name: "Resize debugger details",
	});
	const handleBox = await resizeHandle.boundingBox();
	if (!handleBox) throw new Error("debugger resize handle is not visible");
	await page.mouse.move(
		handleBox.x + handleBox.width / 2,
		handleBox.y + handleBox.height / 2,
	);
	await page.mouse.down();
	await page.mouse.move(handleBox.x - 60, handleBox.y + handleBox.height / 2);
	await page.mouse.up();
	await expect
		.poll(async () => (await details.boundingBox())?.width ?? 0)
		.toBeGreaterThan(initialWidth + 30);
	await page.getByRole("button", { name: "Hide debugger details" }).click();
	await expect(details).toBeHidden();
	await page.getByRole("button", { name: "Show debugger details" }).click();
	await expect(details).toBeVisible();

	await page.getByRole("treeitem", { name: /completion\.go/ }).click();
	await expect(
		page.locator('[data-center-tab="file:completion.go"]'),
	).toBeVisible();
	await expect(
		page.getByRole("toolbar", { name: "Debug controls" }),
	).toBeVisible();
	await expect(page.getByRole("button", { name: "Pause" })).toBeVisible();
	await debugTab.click();
	await page.getByRole("button", { name: "Show terminal" }).click();
	await expect(
		page.getByRole("button", { name: "Show debug output" }),
	).toBeVisible();
	await page.getByRole("button", { name: "Show debug output" }).click();
	await expect(page.getByLabel("Debug output")).toBeVisible();
	await debugTab.hover();
	await page.getByTitle("Close Debug", { exact: true }).click();

	await expect.poll(() => controls.length).toBe(1);
	expect(controls[0]).toMatchObject({
		operation: "stop",
		session_id: session.session_id,
	});
	await expect(debugTab).toHaveCount(0);
});

test("follows debugger stops into the source editor", async ({ page }) => {
	const lines = Array.from({ length: 90 }, (_, index) =>
		index === 69 ? "var breakpointTarget = 70" : `// line ${index + 1}`,
	);
	await writeFile(workspacePath("debug-follow.go"), `${lines.join("\n")}\n`);

	let session = {
		session_id: "debug-follow-session",
		adapter: "delve",
		language: "Go",
		target: "./debug-follow.go",
		mode: "debug",
		request: "launch",
		io: "output",
		capabilities: { supports_step_back: false },
		state_version: 1,
		state: "running",
		stop: undefined as { reason: string; thread_id: number } | undefined,
		started_at: "2026-08-22T10:00:00Z",
	};
	const evaluations: Array<Record<string, unknown>> = [];

	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				git_init: false,
				lsp: false,
				debug: true,
				tasks: false,
				terminal: false,
				platform: "linux",
				workspace_name: "workspace",
			},
		});
	});
	await page.route(/\/api\/debug\/state(?:\?[^#]*)?$/, async (route) => {
		await route.fulfill({
			json: {
				available: true,
				session,
				breakpoints: [],
				frame:
					session.state === "stopped"
						? {
								id: 21,
								name: "main",
								source: { path: "debug-follow.go" },
								line: 70,
								column: 5,
							}
						: undefined,
			},
		});
	});
	await page.route(/\/api\/debug\/evaluate$/, async (route) => {
		evaluations.push(route.request().postDataJSON());
		await route.fulfill({
			json: { result: "70", type: "int", variables_reference: 0 },
		});
	});

	await composer(page);
	await expect(page.locator('[data-center-tab="debug"]')).toBeVisible();
	session = {
		...session,
		state: "stopped",
		state_version: 2,
		stop: { reason: "breakpoint", thread_id: 1 },
	};

	const sourceTab = page.locator('[data-center-tab="file:debug-follow.go"]');
	await expect(sourceTab).toHaveAttribute("aria-selected", "true");
	const stoppedLine = page.locator(".view-line:visible", {
		hasText: "breakpointTarget = 70",
	});
	await expect(stoppedLine).toBeVisible();
	await stoppedLine.hover({ position: { x: 72, y: 8 } });
	await expect.poll(() => evaluations.length).toBe(1);
	expect(evaluations[0]).toMatchObject({
		expression: "breakpointTarget",
		session_id: "debug-follow-session",
		frame_id: 21,
		state_version: 2,
	});
	expect(evaluations[0]).not.toHaveProperty("context");
	await expect(page.locator(".monaco-hover:visible")).toContainText("70");
});

test("recovers an unfinished response in another browser and after reload", async ({
	page,
	context,
}) => {
	const input = await composer(page);
	await input.fill("cancel this request");
	await input.press("Enter");
	await expect(
		page.getByText("Long-running work", { exact: true }),
	).toBeVisible();
	const observer = await context.newPage();
	await observer.goto(page.url());
	await expect(
		observer.getByText("Long-running work", { exact: true }),
	).toBeVisible();
	await page.reload();
	await expect(
		page.getByText("Long-running work", { exact: true }),
	).toBeVisible();
	await observer.getByTitle("Stop (Esc)").click();
	await expect(page.getByTitle("Stop (Esc)")).toHaveCount(0);
	await observer.close();
});

test("retries a lost input receipt without executing the input twice", async ({
	page,
	request,
}) => {
	const input = await composer(page);
	const before = await (
		await request.get(`${controlURL()}/model-stats`)
	).json();
	const requestIDs: string[] = [];
	await page.route(
		"**/api/v2/backends/wingman/sessions/*/commands",
		async (route) => {
			const command = route.request().postDataJSON();
			if (command.type !== "send") {
				await route.continue();
				return;
			}
			requestIDs.push(command.id);
			if (requestIDs.length === 1) {
				await route.fetch();
				await route.abort("failed");
			} else await route.continue();
		},
	);
	await input.fill("render markdown");
	await input.press("Enter");
	await expect(
		page.getByText("Input delivery was not confirmed", { exact: true }),
	).toBeVisible();
	await expect(input).toHaveValue("render markdown");
	await expect(
		page.getByRole("heading", { name: "Migration result" }),
	).toBeVisible();
	await input.press("Enter");
	await expect(input).toHaveValue("");
	expect(requestIDs).toHaveLength(2);
	expect(requestIDs[0]).toBe(requestIDs[1]);
	const after = await (await request.get(`${controlURL()}/model-stats`)).json();
	expect(after.requests - before.requests).toBe(1);
	await expect(
		page
			.getByRole("main")
			.locator("[data-entry-id]")
			.filter({ hasText: /^render markdown$/ }),
	).toHaveCount(1);
});

test("keeps unsent drafts available when the workspace instance changes", async ({
	page,
}) => {
	let changed = false;
	let closeConnection = () => {};
	await page.route(/\/api\/v2\/bootstrap$/, async (route) => {
		const response = await route.fetch();
		const scope = await response.json();
		await route.fulfill({
			json: changed ? { ...scope, instanceId: "replacement" } : scope,
		});
	});
	await page.routeWebSocket(/\/api\/v2\/events/, (socket) => {
		socket.connectToServer();
		closeConnection = () => socket.close();
	});
	const input = await composer(page);
	await input.fill("Keep this unsent draft");
	changed = true;
	closeConnection();
	await expect(
		page.getByRole("button", { name: "Reload workspace" }),
	).toBeVisible();
	await expect(input).toHaveValue("Keep this unsent draft");
});

test("closing the last session leaves empty space and new chat keeps its backend", async ({
	page,
}) => {
	const scope = await (await page.request.get("/api/v2/bootstrap")).json();
	await page.route("**/api/v2/bootstrap", (route) =>
		route.fulfill({
			json: {
				...scope,
				backends: [...scope.backends, { id: "fixture", name: "Fixture" }],
			},
		}),
	);
	await page.route("**/api/v2/backends/fixture/settings", (route) =>
		route.fulfill({ json: emptySession("").settings }),
	);
	await mockSavedSessions(
		page,
		[
			{
				id: "saved",
				title: "Saved session",
				updated_at: new Date().toISOString(),
			},
		],
		[],
		"fixture",
	);
	await page.goto("/fixture/saved");
	const tabs = page.getByRole("tablist", { name: "Open tabs" });
	const saved = tabs.getByRole("tab");
	await expect(saved).toHaveCount(1);
	await saved.press("Delete");
	await expect(tabs.getByRole("tab")).toHaveCount(0);
	await expect(page.locator("[data-empty-workspace]")).toBeVisible();
	await expect(page).toHaveURL(/\/fixture$/);
	await openNewChat(page);
	await expect(
		tabs.locator('[data-center-tab^="draft:fixture:"]'),
	).toBeVisible();
	await expect(page.getByPlaceholder("Message Fixture…")).toBeVisible();
});

test.describe("phone navigation", () => {
	test.use({
		viewport: { width: 390, height: 700 },
		hasTouch: true,
		isMobile: true,
	});

	test("keeps the same draft mounted across phone and desktop layouts", async ({
		page,
	}) => {
		const input = await composer(page);
		await expect(page.locator("[data-mobile-navigation]")).toBeVisible();
		await expect(
			page.getByRole("tree", { name: "Workspace files" }),
		).not.toBeVisible();
		await expect(
			page.getByRole("combobox", { name: "Agent", exact: true }),
		).toBeVisible();
		await input.fill("Keep this draft through rotation and resizing");
		await input.evaluate((element) =>
			element.setAttribute("data-original-composer", "yes"),
		);
		expect(
			await input.evaluate((element) => getComputedStyle(element).fontSize),
		).toBe("16px");
		for (const width of [820, 1200, 390]) {
			await page.setViewportSize({ width, height: 700 });
			await expect(input).toHaveValue(
				"Keep this draft through rotation and resizing",
			);
			await expect(input).toHaveAttribute("data-original-composer", "yes");
			if (width <= 1024)
				await expect(page.locator("[data-mobile-navigation]")).toBeVisible();
			else {
				await expect(page.locator("[data-window-titlebar]")).toBeVisible();
				await expect(page.locator("[data-mobile-navigation]")).toHaveCount(0);
			}
		}
		const box = await input.boundingBox();
		expect(box!.x).toBeGreaterThanOrEqual(0);
		expect(box!.x + box!.width).toBeLessThanOrEqual(390);
		await page
			.getByRole("button", { name: "Show sessions", exact: true })
			.tap();
		await expect(
			page.getByRole("dialog", { name: "Sessions", exact: true }),
		).toBeVisible();
		await page
			.getByRole("button", { name: "Close sessions", exact: true })
			.tap();
		await expect(input).toHaveValue(
			"Keep this draft through rotation and resizing",
		);
	});

	test("switches agents and opens the selected agent's sessions without nested popups", async ({
		page,
	}) => {
		const scope = await (await page.request.get("/api/v2/bootstrap")).json();
		await page.route("**/api/v2/bootstrap", (route) =>
			route.fulfill({
				json: {
					...scope,
					backends: [
						...scope.backends,
						{ id: "fixture", name: "Fixture" },
						...Array.from({ length: 20 }, (_, index) => ({
							id: `agent-${index}`,
							name: `Agent ${index}`,
						})),
					],
				},
			}),
		);
		await page.route("**/api/v2/backends/fixture/settings", (route) =>
			route.fulfill({ json: emptySession("").settings }),
		);
		await mockSavedSessions(
			page,
			[
				{
					id: "phone-saved",
					title: "From another browser",
					updated_at: new Date().toISOString(),
				},
			],
			[],
			"fixture",
		);
		await composer(page);
		const picker = page.getByRole("combobox", { name: "Agent", exact: true });
		await expect(picker).toBeVisible();
		const box = await picker.boundingBox();
		expect(box!.height).toBeGreaterThanOrEqual(44);
		await picker.selectOption("fixture");
		await expect(page).toHaveURL(/\/fixture$/);
		await expect(picker).toHaveValue("fixture");
		await page
			.getByRole("button", { name: "Show sessions", exact: true })
			.tap();
		const dialog = page.getByRole("dialog", { name: "Sessions", exact: true });
		await expect(dialog).toBeVisible();
		await dialog.getByRole("button", { name: /From another browser/ }).tap();
		await expect(dialog).not.toBeVisible();
		await expect(page).toHaveURL(/\/fixture\/phone-saved$/);
		await expect(picker).toHaveValue("fixture");
		await expect(page.getByPlaceholder("Message Fixture…")).toBeVisible();
	});

	test("keeps model controls inside the viewport and above the chat", async ({
		page,
	}) => {
		await composer(page);
		const trigger = page.locator(
			'[data-chat-composer] button[aria-haspopup="dialog"]',
		);
		await expect(trigger).toBeVisible();
		await trigger.tap();
		const picker = page.getByRole("dialog", {
			name: "Model and reasoning effort",
		});
		await expect(picker).toBeVisible();
		await expectFloatingInViewport(page, picker);
		expect(
			await picker.evaluate((element) => {
				const box = element.getBoundingClientRect();
				return element.contains(
					document.elementFromPoint(
						box.x + box.width / 2,
						box.y + box.height / 2,
					),
				);
			}),
		).toBe(true);
		await page.screenshot({
			path: test.info().outputPath("phone-model-picker.png"),
		});
		await page.keyboard.press("Escape");
		await expect(picker).not.toBeVisible();
	});
});

test.describe("remote access", () => {
	test.use({
		viewport: { width: 390, height: 700 },
		hasTouch: true,
		isMobile: true,
	});

	test("pairs a phone and shares the running session with a local browser", async ({
		page,
		context,
	}) => {
		const pairURL = process.env.E2E_PAIR_URL!;
		const requestURLs: string[] = [];
		page.on("request", (request) => requestURLs.push(request.url()));
		await page.goto(pairURL);
		const input = page.getByPlaceholder("Message Wingman…");
		await expect(input).toBeVisible();
		await expect(page.locator("[data-mobile-navigation]")).toBeVisible();
		const secret = new URL(pairURL).hash.split(".")[1];
		expect(requestURLs.some((url) => url.includes(secret))).toBe(false);
		const cookies = await context.cookies(process.env.E2E_RELAY_URL!);
		expect(
			cookies.find((cookie) => cookie.name === "wingman_remote")?.httpOnly,
		).toBe(true);
		await input.fill("cancel this request");
		await input.press("Enter");
		await expect(
			page.getByText("Long-running work", { exact: true }),
		).toBeVisible();
		const observer = await context.newPage();
		await observer.goto(
			new URL(new URL(page.url()).pathname, process.env.E2E_BASE_URL!).href,
		);
		await expect(
			observer.getByText("Long-running work", { exact: true }),
		).toBeVisible();
		await page.reload();
		await expect(
			page.getByText("Long-running work", { exact: true }),
		).toBeVisible();
		await expect
			.poll(
				async () =>
					(await page.locator("[data-mobile-navigation]").boundingBox())?.y ??
					-1,
			)
			.toBeGreaterThanOrEqual(0);
		await page.screenshot({ path: test.info().outputPath("remote-phone.png") });
		await observer.getByTitle("Stop (Esc)").tap();
		await expect(page.getByTitle("Stop (Esc)")).toHaveCount(0);
		await observer.close();
	});

	test("retries a lost receipt through the relay without another model invocation", async ({
		page,
		request,
	}) => {
		await page.goto(process.env.E2E_PAIR_URL!);
		const input = page.getByPlaceholder("Message Wingman…");
		await expect(input).toBeVisible();
		const before = await (
			await request.get(`${controlURL()}/model-stats`)
		).json();
		const ids: string[] = [];
		await page.route(
			"**/api/v2/backends/wingman/sessions/*/commands",
			async (route) => {
				const command = route.request().postDataJSON();
				if (command.type !== "send") return route.continue();
				ids.push(command.id);
				if (ids.length === 1) {
					await route.fetch();
					await route.abort("failed");
				} else await route.continue();
			},
		);
		await input.fill("render markdown");
		await input.press("Enter");
		await expect(
			page.getByText("Input delivery was not confirmed", { exact: true }),
		).toBeVisible();
		await expect(input).toHaveValue("render markdown");
		await expect(
			page.getByRole("heading", { name: "Migration result" }),
		).toBeVisible();
		await input.press("Enter");
		await expect(input).toHaveValue("");
		expect(ids).toHaveLength(2);
		expect(ids[0]).toBe(ids[1]);
		const after = await (
			await request.get(`${controlURL()}/model-stats`)
		).json();
		expect(after.requests - before.requests).toBe(1);
	});
});
