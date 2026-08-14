import { expect, test, type Locator, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";

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
	await page.getByLabel("Show sessions").click();
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
	await page.getByLabel("Show sessions").click();

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
	await page.keyboard.type("package main\n\nfunc initialVersion() {}\n");
	const save = page.getByRole("button", { name: "Save file" });
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toHaveAttribute("title", "No changes to save");

	let read = await request.get("/api/files/read?path=web-created.go");
	expect(read.ok()).toBeTruthy();
	let file = (await read.json()) as { content: string; revision: string };
	expect(file.content).toContain("initialVersion");

	const refreshedContent = "package main\n\nfunc refreshedFromDisk() {}\n";
	await writeFile(workspacePath("web-created.go"), refreshedContent);
	await expect(page.locator(".view-lines")).toContainText("refreshedFromDisk");

	await editor.click();
	await page.keyboard.press("Control+End");
	await page.keyboard.type("\nfunc localVersion() {}\n");
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

	await save.click();
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
	await expect(save).toBeEnabled();
	await save.click();
	await expect(save).toHaveAttribute("title", "No changes to save");
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
	await page.getByLabel("Show sessions").click();
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
			getComputedStyle(element).getPropertyValue("--shell-window-drag").trim(),
		),
	).toBe("drag");
	expect(
		await toolbar
			.getByLabel(/sessions/)
			.evaluate((element) =>
				getComputedStyle(element)
					.getPropertyValue("--shell-window-drag")
					.trim(),
			),
	).toBe("no-drag");
	expect(
		await toolbar
			.locator("[data-titlebar-left-panel]")
			.evaluate((element) =>
				getComputedStyle(element)
					.getPropertyValue("--shell-window-drag")
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
		"0px",
	);
	await expect(page.locator('[data-layout-panel="workspace"]')).toHaveCSS(
		"width",
		"304px",
	);
	const sessionsFrame = page.locator('[data-panel-frame="sessions"]');
	await expect(sessionsFrame).toHaveCSS("width", "240px");
	await expect(sessionsFrame).toHaveCSS("opacity", "0");
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
		"40px",
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
	await toolbar.getByRole("button", { name: "Show sessions" }).click();
	await expect(page.locator('[data-layout-panel="sessions"]')).toHaveCSS(
		"width",
		"240px",
	);
	await expect(sessionsFrame).toHaveCSS("opacity", "1");
	await expect(toolbar.locator("[data-titlebar-left-panel]")).toHaveCSS(
		"width",
		"240px",
	);
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
	await toolbar.getByRole("button", { name: "Show sessions" }).click();
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
		await expect(page.locator('[data-layout-panel="sessions"]')).toHaveCount(1);
		await expect(page.locator('[data-layout-panel="workspace"]')).toHaveCount(
			1,
		);
		await expect(page.getByRole("dialog", { name: "Sessions" })).toHaveCount(0);
		await expect(page.getByRole("dialog", { name: "Workspace" })).toHaveCount(
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

test("uses Wingman's dynamic editor context menu", async ({ page }) => {
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
	for (const item of [
		"Peek Definition",
		"Peek Type Definition",
		"Peek Implementations",
	]) {
		await expect(
			menu.getByRole("menuitem", { name: item, exact: true }),
		).toHaveCount(0);
	}

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
	for (const item of [
		"Go to Definition",
		"Go to Type Definition",
		"Go to Implementations",
	]) {
		await expect(
			menu.getByRole("menuitem", { name: item, exact: true }),
		).toHaveCount(0);
	}
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
	await editor.focus();
	await page.keyboard.press("Shift+F10");
	await expect(menu).toBeVisible();
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
	const newFile = page.getByRole("dialog", { name: "New File" });
	await expect(newFile).toBeVisible();
	await newFile
		.getByRole("textbox", { name: "File path" })
		.fill("menu-created.txt");
	await newFile.getByRole("button", { name: "Create" }).click();

	await expect(
		page.getByRole("tab", { name: "menu-created.txt" }),
	).toBeVisible();
	await expect.poll(() => commandEnabled("save")).toBe(true);
	await expect.poll(() => commandEnabled("save-as")).toBe(true);
	const editor = page.locator(".monaco-editor");
	await editor.click();
	await page.keyboard.type("created through the File menu\n");

	await page.evaluate(() =>
		window.dispatchEvent(
			new CustomEvent("shell:command", { detail: "save-as" }),
		),
	);
	const saveAs = page.getByRole("dialog", { name: "Save As" });
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

	await expect(page.locator('[data-layout-panel="sessions"]')).toHaveCount(1);
	await expect(page.locator('[data-layout-panel="workspace"]')).toHaveCount(1);
	await expect(page.getByRole("dialog", { name: "Sessions" })).toHaveCount(0);
	await expect(page.getByRole("dialog", { name: "Workspace" })).toHaveCount(0);

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
