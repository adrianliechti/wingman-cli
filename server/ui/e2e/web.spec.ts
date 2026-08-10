import { expect, test, type Page } from "@playwright/test";
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

test("places navigation, tabs, and contextual actions in one window toolbar", async ({
	page,
}) => {
	await composer(page);
	const toolbar = page.getByLabel("Window toolbar");
	const tabStrip = page.getByRole("tablist", { name: "Open tabs" });

	await expect(toolbar).toBeVisible();
	await expect(toolbar).toHaveCSS("height", "40px");
	await expect(toolbar).toHaveCSS("border-bottom-width", "1px");
	await expect(toolbar.locator(".window-titlebar-controls-spacer")).toHaveCSS(
		"width",
		"0px",
	);
	await expect(tabStrip).toBeVisible();
	await expect(tabStrip).toHaveCSS("border-bottom-width", "0px");
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
	await expect(page.locator('[data-layout-panel="sessions"]')).toHaveCSS(
		"border-right-width",
		"1px",
	);
	await expect(page.locator('[data-layout-panel="workspace"]')).toHaveCSS(
		"border-left-width",
		"1px",
	);
	await expect(page.getByLabel("Resize panels")).toHaveCount(0);
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
			const firstTab = await tabStrip.getByRole("tab").first().boundingBox();
			if (!titlebarPanel || !sidebar || !firstTab) return false;
			const divider = sidebar.x + sidebar.width;
			return (
				Math.abs(titlebarPanel.x + titlebarPanel.width - divider) <= 1 &&
				Math.abs(firstTab.x - divider) <= 1
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
		await toolbar.getByRole("button", { name: "Hide sessions" }).click();
		await expect(toolbar.getByTitle(/^Agent:/)).toHaveCount(0);
		await toolbar.getByRole("button", { name: "Show sessions" }).click();
		await expect(toolbar.getByTitle(/^Agent:/)).toBeVisible();
	}
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
		await page.getByRole("button", { name: /default/ }).click();
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
