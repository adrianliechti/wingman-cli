import { expect, test, type Page } from "@playwright/test";

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
	await expect(page.getByTitle("e2e-title")).toBeVisible({ timeout: 15_000 });

	const shellMenu = page.getByTitle("New terminal with another shell");
	if (await shellMenu.count()) {
		await shellMenu.click();
		await page.getByRole("button", { name: /default/ }).click();
		await expect(page.locator(".xterm-screen")).toHaveCount(1);
		await page.getByTitle("e2e-title").click();
		await expect(page.locator(".xterm-screen")).toBeVisible();
	}

	await expect(page.getByTitle("Hide terminal", { exact: true })).toHaveCount(
		0,
	);
});
