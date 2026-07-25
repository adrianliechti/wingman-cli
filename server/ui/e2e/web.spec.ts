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
