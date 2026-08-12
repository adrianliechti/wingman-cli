import { expect, test, type Locator, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

function controlURL(): string {
	const url = process.env.E2E_CONTROL_URL;
	if (!url) throw new Error("E2E_CONTROL_URL is required");
	return url;
}

async function composer(page: Page) {
	await page.goto("/");
	const input = page.getByPlaceholder("Message Wingman…");
	await expect(input).toBeVisible();
	return input;
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

test("keeps the active tab visible as the tab strip fills", async ({
	page,
}) => {
	await composer(page);
	const tabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.getByRole("tab");
	const initialCount = await tabs.count();
	for (let index = 0; index < 6; index++) {
		await page.getByTitle(/New .* terminal/).click();
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
});

test("keeps the empty draft tab non-closable", async ({ page }) => {
	await composer(page);
	const tabs = page
		.getByRole("tablist", { name: "Open tabs" })
		.getByRole("tab");
	const draft = tabs.first();
	const draftId = await draft.getAttribute("data-center-tab");
	expect(draftId).toBeTruthy();

	await expect(draft).toHaveAttribute("aria-label", /New Session/);
	await draft.hover();
	await expect(draft.locator("[data-tab-close]")).toHaveCount(0);
	await draft.focus();
	await page.keyboard.press("Delete");
	await expect(page.locator(`[data-center-tab="${draftId}"]`)).toHaveCount(1);
});

test("uses canonical product names for agents", async ({ page }) => {
	await page.route(/\/api\/agents$/, async (route) => {
		await route.fulfill({
			json: [
				{ id: "wingman", name: "Wingman" },
				{ id: "claude", name: "claude" },
				{ id: "codex", name: "codex" },
				{ id: "copilot", name: "copilot" },
				{ id: "opencode", name: "opencode" },
				{ id: "pi", name: "pi" },
			],
		});
	});
	await page.route(/\/api\/agent$/, async (route) => {
		await route.fulfill({ json: { agent: "opencode" } });
	});

	await composer(page);
	const picker = page.getByTitle("Agent: OpenCode");
	await expect(picker).toBeVisible();
	await picker.click();

	const menu = page.getByRole("menu", { name: "Agent" });
	for (const name of ["Claude", "Codex", "Copilot", "OpenCode", "Pi"]) {
		await expect(
			menu.getByRole("menuitemradio", { name, exact: true }),
		).toBeVisible();
	}
});

test("uses each Git status slot for its stage action", async ({ page }) => {
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: true,
				lsp: false,
				diffs: true,
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
	await page.route(/\/api\/git\/branches/, async (route) => {
		await route.fulfill({
			json: {
				branches: [{ name: "main", current: true, remote: "" }],
				warning: "",
			},
		});
	});
	await composer(page);
	await page
		.getByRole("tablist", { name: "Workspace panels" })
		.getByRole("tab", { name: "Changes", exact: true })
		.click();

	await page.getByTitle("main", { exact: true }).click();
	const branches = page.getByRole("dialog", { name: "Switch Git branch" });
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

test("keeps the session context menu above panel clipping", async ({
	page,
}) => {
	await page.route(/\/api\/sessions$/, async (route) => {
		await route.fulfill({
			json: [
				{
					id: "menu-clipping-check",
					title: "Menu clipping check",
					created_at: "2026-08-11T00:00:00Z",
					updated_at: "2026-08-11T00:00:00Z",
				},
			],
		});
	});
	await page.route(/\/api\/agent$/, async (route) => {
		await route.fulfill({ json: { agent: "wingman", canDelete: true } });
	});
	await composer(page);

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
	await composer(page);
	const file = page.getByRole("treeitem", { name: /editable\.txt/ });
	await file.click({ button: "right" });

	const menu = page.getByRole("menu", { name: "Actions for editable.txt" });
	await expect(menu).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Rename" })).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Duplicate" })).toBeVisible();
	await expect(menu.getByRole("menuitem", { name: "Delete" })).toBeVisible();
	await expectFloatingInViewport(page, menu);
	await expect(menu.getByRole("menuitem", { name: "Open" })).toBeFocused();
	await page.keyboard.press("ArrowDown");
	await expect(menu.getByRole("menuitem", { name: "Copy" })).toBeFocused();
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
	await page.route(/\/api\/sessions$/, async (route) => {
		await route.fulfill({
			json: [
				{
					id: "agent-menu-check",
					title: "Agent menu check",
					created_at: "2026-08-11T00:00:00Z",
					updated_at: "2026-08-11T00:00:00Z",
				},
			],
		});
	});
	await page.route(
		/\/api\/sessions\/agent-menu-check\/load$/,
		async (route) => {
			await route.fulfill({ status: 204 });
		},
	);
	await page.route(/\/api\/capabilities$/, async (route) => {
		await route.fulfill({
			json: {
				git: false,
				lsp: false,
				diffs: false,
				tasks: true,
				terminal: true,
			},
		});
	});
	await page.route(/\/api\/sessions\/[^/]+\/tasks$/, async (route) => {
		await route.fulfill({ json: [] });
	});
	await page.route(/\/api\/sessions\/[^/]+\/schedules$/, async (route) => {
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
	});
	await composer(page);
	await page.getByTitle("Agent menu check").click();
	await page
		.getByRole("tablist", { name: "Workspace panels" })
		.getByRole("tab", { name: "Agents", exact: true })
		.click();

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

test("places navigation, tabs, and contextual actions in one window toolbar", async ({
	page,
}) => {
	await composer(page);
	const toolbar = page.getByLabel("Window toolbar");
	const tabStrip = page.getByRole("tablist", { name: "Open tabs" });

	await expect(toolbar).toBeVisible();
	await expect(toolbar).toHaveCSS("height", "40px");
	await expect(toolbar).toHaveCSS("border-bottom-width", "0px");
	await expect(toolbar.locator("[data-titlebar-separator]")).toHaveCSS(
		"height",
		"1px",
	);
	await expect(toolbar.locator(".window-titlebar-controls-spacer")).toHaveCSS(
		"width",
		"0px",
	);
	await expect(tabStrip).toBeVisible();
	await expect(tabStrip).toHaveCSS("border-bottom-width", "0px");
	await expect(tabStrip).toHaveCSS("scrollbar-width", "none");
	await expect(tabStrip).toHaveCSS("overscroll-behavior-x", "contain");
	expect(
		await toolbar.evaluate((element) =>
			getComputedStyle(element)
				.getPropertyValue("--wingman-window-drag")
				.trim(),
		),
	).toBe("drag");
	expect(
		await toolbar
			.getByLabel(/sessions/)
			.evaluate((element) =>
				getComputedStyle(element)
					.getPropertyValue("--wingman-window-drag")
					.trim(),
			),
	).toBe("no-drag");
	expect(
		await toolbar
			.locator("[data-titlebar-left-panel]")
			.evaluate((element) =>
				getComputedStyle(element)
					.getPropertyValue("--wingman-window-drag")
					.trim(),
			),
	).toBe("drag");
	expect(
		await tabStrip.evaluate((element) =>
			element.closest("[data-window-titlebar]")?.getAttribute("aria-label"),
		),
	).toBe("Window toolbar");
	await expect(toolbar.getByLabel(/sessions/)).toBeVisible();
	await expect(toolbar.getByLabel(/workspace panel/)).toBeVisible();
	await expect(page.locator('[data-layout-panel="sessions"]')).toHaveCSS(
		"width",
		"240px",
	);
	await expect(page.locator('[data-layout-panel="workspace"]')).toHaveCSS(
		"width",
		"304px",
	);
	const sessionsFrame = page.locator('[data-panel-frame="sessions"]');
	await expect(sessionsFrame).toHaveCSS("width", "240px");
	await expect(sessionsFrame).toHaveCSS("border-radius", "10px");
	await expect(sessionsFrame).toHaveCSS("border-right-width", "0px");
	const workspaceFrame = page.locator('[data-panel-frame="workspace"]');
	await expect(workspaceFrame).toHaveCSS("width", "304px");
	await expect(workspaceFrame).toHaveCSS("border-radius", "10px");
	await expect(workspaceFrame).toHaveCSS("border-left-width", "0px");
	await expect(
		page.getByRole("separator", { name: /Resize .* panel/ }),
	).toHaveCount(2);
	await expect(toolbar.locator("[data-titlebar-left-panel]")).toHaveCSS(
		"width",
		"240px",
	);
	await expect(toolbar.locator("[data-titlebar-right-panel]")).toHaveCSS(
		"width",
		"304px",
	);
	const workspaceTabs = page.getByRole("tablist", {
		name: "Workspace panels",
	});
	await expect(workspaceTabs).toBeVisible();
	expect(
		await workspaceTabs.evaluate((element) =>
			element
				.closest("[data-window-titlebar]")
				?.hasAttribute("data-window-titlebar"),
		),
	).toBe(true);
	await expect(
		page
			.locator('[data-panel-content="workspace"]')
			.getByRole("tablist", { name: "Workspace panels" }),
	).toHaveCount(0);
	await expect
		.poll(async () => {
			const titlebarPanel = await toolbar
				.locator("[data-titlebar-left-panel]")
				.boundingBox();
			const sidebar = await page
				.locator('[data-layout-panel="sessions"]')
				.boundingBox();
			const frame = await sessionsFrame.boundingBox();
			const firstTab = await tabStrip.getByRole("tab").first().boundingBox();
			const toggle = await toolbar
				.getByRole("button", { name: "Hide sessions" })
				.boundingBox();
			if (!titlebarPanel || !sidebar || !frame || !firstTab || !toggle)
				return false;
			const divider = sidebar.x + sidebar.width;
			return (
				Math.abs(titlebarPanel.x + titlebarPanel.width - divider) <= 1 &&
				Math.abs(frame.x - sidebar.x) <= 1 &&
				Math.abs(frame.x + frame.width - divider) <= 1 &&
				Math.abs(toggle.x + toggle.width - divider) <= 1 &&
				Math.abs(firstTab.x - divider) <= 1
			);
		})
		.toBe(true);
	await expect
		.poll(async () => {
			const titlebarPanel = await toolbar
				.locator("[data-titlebar-right-panel]")
				.boundingBox();
			const workspace = await page
				.locator('[data-layout-panel="workspace"]')
				.boundingBox();
			const frame = await workspaceFrame.boundingBox();
			const toggle = await toolbar
				.getByRole("button", { name: "Hide workspace panel" })
				.boundingBox();
			if (!titlebarPanel || !workspace || !frame || !toggle) return false;
			return (
				Math.abs(titlebarPanel.x - workspace.x) <= 1 &&
				Math.abs(frame.x - workspace.x) <= 1 &&
				Math.abs(toggle.x - workspace.x) <= 1 &&
				Math.abs(frame.x + frame.width - (workspace.x + workspace.width)) <= 1
			);
		})
		.toBe(true);
	await expect(toolbar).toHaveCSS("border-radius", "0px");
	await expect(toolbar.locator("[data-titlebar-left-panel]")).toHaveCSS(
		"border-right-width",
		"0px",
	);

	const agentChooser = toolbar.getByTitle(/^Agent:/);
	if (await agentChooser.count()) {
		await expect(agentChooser).toBeVisible();
		const sessions = page.locator('[data-layout-panel="sessions"]');
		const sessionsContent = page.locator('[data-panel-content="sessions"]');
		await toolbar.getByRole("button", { name: "Hide sessions" }).click();
		await expect(toolbar.locator("[data-titlebar-agent]")).toHaveCount(0);
		await expect(sessions).toHaveCSS("width", "0px");
		await expect(sessionsContent).toHaveCSS("width", "240px");
		await expect(sessionsContent).toHaveCSS("opacity", "0");
		await expect(sessionsFrame).toHaveCSS("opacity", "0");
		await toolbar.getByRole("button", { name: "Show sessions" }).click();
		await expect(toolbar.getByTitle(/^Agent:/)).toBeVisible();
		await expect(sessions).toHaveCSS("width", "240px");
		await expect(sessionsContent).toHaveCSS("width", "240px");
		await expect(sessionsContent).toHaveCSS("opacity", "1");
		await expect(sessionsFrame).toHaveCSS("opacity", "1");
	}
	const workspace = page.locator('[data-layout-panel="workspace"]');
	const workspaceContent = page.locator('[data-panel-content="workspace"]');
	await toolbar.getByRole("button", { name: "Hide workspace panel" }).click();
	await expect(workspace).toHaveCSS("width", "0px");
	await expect(workspaceContent).toHaveCSS("width", "304px");
	await expect(workspaceContent).toHaveCSS("opacity", "0");
	await expect(workspaceFrame).toHaveCSS("opacity", "0");
	await expect(toolbar.locator("[data-titlebar-right-panel]")).toHaveCSS(
		"width",
		"40px",
	);
	await expect(workspaceTabs).toHaveCount(0);
	await toolbar.getByRole("button", { name: "Show workspace panel" }).click();
	await expect(workspace).toHaveCSS("width", "304px");
	await expect(workspaceContent).toHaveCSS("width", "304px");
	await expect(workspaceContent).toHaveCSS("opacity", "1");
	await expect(workspaceFrame).toHaveCSS("opacity", "1");
	await expect(toolbar.locator("[data-titlebar-right-panel]")).toHaveCSS(
		"width",
		"304px",
	);
	await expect(workspaceTabs).toBeVisible();
	const newSession = toolbar.getByRole("button", { name: "New session" });
	if (await newSession.count()) {
		expect(
			await newSession.evaluate((element) =>
				element
					.closest("[data-titlebar-actions]")
					?.hasAttribute("data-titlebar-actions"),
			),
		).toBe(true);
	}
});

test("resizes borderless desktop panels within their limits", async ({
	page,
}) => {
	await composer(page);
	const toolbar = page.getByLabel("Window toolbar");
	const sessions = page.locator('[data-layout-panel="sessions"]');
	const workspace = page.locator('[data-layout-panel="workspace"]');
	const sessionsHandle = page.getByRole("separator", {
		name: "Resize sessions panel",
	});
	const workspaceHandle = page.getByRole("separator", {
		name: "Resize workspace panel",
	});

	await expect(sessionsHandle).toHaveCSS(
		"background-color",
		"rgba(0, 0, 0, 0)",
	);
	await sessionsHandle.hover();
	await expect(sessionsHandle).toHaveCSS(
		"background-color",
		"rgba(0, 0, 0, 0)",
	);

	const sessionsBox = await sessionsHandle.boundingBox();
	expect(sessionsBox).not.toBeNull();
	await page.mouse.move(
		sessionsBox!.x + sessionsBox!.width / 2,
		sessionsBox!.y + sessionsBox!.height / 2,
	);
	await page.mouse.down();
	await page.mouse.move(
		sessionsBox!.x + sessionsBox!.width / 2 + 60,
		sessionsBox!.y + 20,
		{ steps: 5 },
	);
	await expect(sessions).toHaveCSS("width", "300px");
	await expect(toolbar.locator("[data-titlebar-left-panel]")).toHaveCSS(
		"width",
		"300px",
	);
	await page.mouse.move(sessionsBox!.x + 1_000, sessionsBox!.y + 20);
	await expect(sessions).toHaveCSS("width", "360px");
	await page.mouse.up();
	await expect(page.locator('[data-panel-frame="sessions"]')).toHaveCSS(
		"width",
		"360px",
	);

	const workspaceBox = await workspaceHandle.boundingBox();
	expect(workspaceBox).not.toBeNull();
	await page.mouse.move(
		workspaceBox!.x + workspaceBox!.width / 2,
		workspaceBox!.y + workspaceBox!.height / 2,
	);
	await page.mouse.down();
	await page.mouse.move(
		workspaceBox!.x + workspaceBox!.width / 2 - 60,
		workspaceBox!.y + 20,
		{ steps: 5 },
	);
	await expect(workspace).toHaveCSS("width", "364px");
	await expect(toolbar.locator("[data-titlebar-right-panel]")).toHaveCSS(
		"width",
		"364px",
	);
	await page.mouse.move(workspaceBox!.x - 1_000, workspaceBox!.y + 20);
	await expect(workspace).toHaveCSS("width", "480px");
	await page.mouse.up();
	await expect(page.locator('[data-panel-frame="workspace"]')).toHaveCSS(
		"width",
		"480px",
	);
});

test("keeps the center workspace mounted across responsive layouts", async ({
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
	}
});

test("keeps workspace tabs inside the drawer on narrow layouts", async ({
	page,
}) => {
	await page.setViewportSize({ width: 700, height: 800 });
	await composer(page);
	const toolbar = page.getByLabel("Window toolbar");
	await toolbar.getByRole("button", { name: "Show workspace panel" }).click();
	const workspaceTabs = page.getByRole("tablist", {
		name: "Workspace panels",
	});
	await expect(workspaceTabs).toBeVisible();
	expect(
		await workspaceTabs.evaluate(
			(element) => element.closest("[data-window-titlebar]") === null,
		),
	).toBe(true);
});

test("runs a coding tool and renders its result", async ({ page }) => {
	const input = await composer(page);
	await input.fill("create e2e-result.txt");
	await input.press("Enter");

	await page.getByRole("button", { name: "1 tool" }).click();
	const tool = page.getByText("write", { exact: true });
	await expect(tool).toBeVisible();
	await tool.click();
	await expect(page.getByText(/Created .*e2e-result\.txt/)).toBeVisible();
	await expect(
		page.getByText("Created the requested file", { exact: true }),
	).toBeVisible();

	const usage = page.getByRole("button", {
		name: /context left|token usage/i,
	});
	await expect(usage).toBeVisible();
	await expect(usage).toHaveAttribute("title", /input|context/i);
	await usage.click();
	const usageDialog = page.getByRole("dialog", { name: "Usage information" });
	await expect(
		usageDialog.getByText("Input tokens", { exact: true }),
	).toBeVisible();
	await expect(
		usageDialog.getByText("Output tokens", { exact: true }),
	).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(usageDialog).toBeHidden();

	await page.getByRole("treeitem", { name: "e2e-result.txt" }).click();
	await expect(usage).toBeHidden();
	await page.getByRole("tab", { name: /create e2e-result\.txt/i }).click();
	await expect(usage).toBeVisible();
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

	const mermaidBlock = markdown.locator(
		'[data-markdown-code][data-language="mermaid"]',
	);
	await expect(mermaidBlock).toContainText("graph TD; A-->B");
	await expect(mermaidBlock.locator("pre > code")).toBeVisible();
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

	await page.getByTitle(/New .* terminal/).click();

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

	const shellMenu = page.getByTitle("New terminal with another shell");
	if (await shellMenu.count()) {
		await shellMenu.click();
		await page.getByRole("menuitem", { name: /default/ }).click();
		await expect(page.locator(".xterm-screen")).toHaveCount(1);
		await terminalTab.click();
		await expect(page.locator(".xterm-screen")).toBeVisible();
	}

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

test("uses accessible responsive drawers and command navigation", async ({
	page,
}) => {
	await page.setViewportSize({ width: 720, height: 800 });
	await composer(page);

	await page.getByLabel("Show sessions").click();
	const sessions = page.getByRole("dialog", { name: "Sessions" });
	await expect(sessions).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(sessions).toBeHidden();

	await page.getByLabel("Show workspace panel").click();
	const workspace = page.getByRole("dialog", { name: "Workspace" });
	await expect(workspace).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(workspace).toBeHidden();

	await page.keyboard.press("Control+k");
	await expect(
		page.getByRole("dialog", { name: "Command palette" }),
	).toBeVisible();
	await expect(
		page.getByRole("combobox", { name: "Search commands" }),
	).toBeFocused();
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
