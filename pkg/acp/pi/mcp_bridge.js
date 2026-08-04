import { spawn } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { basename } from "node:path";
import { pathToFileURL } from "node:url";

const config = JSON.parse(readFileSync(process.env.WINGMAN_PI_MCP_CONFIG, "utf8"));
const clients = [];

function writeStatus(value) {
	writeFileSync(config.statusPath, JSON.stringify(value), { mode: 0o600 });
}

function errorText(error) {
	return error instanceof Error ? error.message : String(error);
}

class StdioMCPClient {
	constructor(server) {
		this.server = server;
		this.nextId = 1;
		this.pending = new Map();
		this.buffer = "";
		this.closed = false;
	}

	async start() {
		this.child = spawn(this.server.command, this.server.args ?? [], {
			cwd: config.cwd,
			env: { ...process.env, ...(this.server.env ?? {}) },
			stdio: ["pipe", "pipe", "pipe"],
		});
		this.child.stdout.setEncoding("utf8");
		this.child.stdout.on("data", (chunk) => this.onData(chunk));
		this.child.stderr.setEncoding("utf8");
		this.child.stderr.on("data", (chunk) => process.stderr.write(`[pi-acp MCP ${this.server.name}] ${chunk}`));
		this.child.on("error", (error) => this.fail(error));
		this.child.on("exit", (code, signal) => {
			this.fail(new Error(`server exited (${signal ? `signal ${signal}` : `code ${code}`})`));
		});

		const initialized = await this.request("initialize", {
			protocolVersion: "2024-11-05",
			capabilities: { roots: { listChanged: false } },
			clientInfo: { name: "wingman-pi-acp", version: "0.1.0" },
		});
		if (!initialized?.protocolVersion) {
			throw new Error(`${this.server.name}: initialize returned no protocol version`);
		}
		this.notify("notifications/initialized", {});
	}

	onData(chunk) {
		this.buffer += chunk;
		for (;;) {
			const newline = this.buffer.indexOf("\n");
			if (newline < 0) return;
			const line = this.buffer.slice(0, newline).trim();
			this.buffer = this.buffer.slice(newline + 1);
			if (!line) continue;
			try {
				this.onMessage(JSON.parse(line));
			} catch (error) {
				process.stderr.write(`[pi-acp MCP ${this.server.name}] invalid JSON: ${errorText(error)}\n`);
			}
		}
	}

	onMessage(message) {
		if (message.id !== undefined && message.id !== null && !message.method) {
			const pending = this.pending.get(String(message.id));
			if (!pending) return;
			this.pending.delete(String(message.id));
			pending.cleanup();
			if (message.error) {
				pending.reject(new Error(`${this.server.name}: ${message.error.message ?? "MCP request failed"}`));
			} else {
				pending.resolve(message.result);
			}
			return;
		}

		if (message.method && message.id !== undefined && message.id !== null) {
			if (message.method === "roots/list") {
				this.send({
					jsonrpc: "2.0",
					id: message.id,
					result: { roots: [{ uri: pathToFileURL(config.cwd).href, name: basename(config.cwd) || config.cwd }] },
				});
			} else if (message.method === "ping") {
				this.send({ jsonrpc: "2.0", id: message.id, result: {} });
			} else {
				this.send({
					jsonrpc: "2.0",
					id: message.id,
					error: { code: -32601, message: `Client method not supported: ${message.method}` },
				});
			}
		}
	}

	send(message) {
		if (this.closed || !this.child?.stdin?.writable) throw new Error(`${this.server.name}: MCP server is closed`);
		this.child.stdin.write(`${JSON.stringify(message)}\n`);
	}

	notify(method, params) {
		this.send({ jsonrpc: "2.0", method, params });
	}

	request(method, params = {}, signal) {
		if (signal?.aborted) return Promise.reject(new Error("MCP request cancelled"));
		const id = this.nextId++;
		return new Promise((resolve, reject) => {
			const onAbort = () => {
				this.pending.delete(String(id));
				try {
					this.notify("notifications/cancelled", { requestId: id, reason: "Cancelled by ACP client" });
				} catch {}
				reject(new Error("MCP request cancelled"));
			};
			const cleanup = () => signal?.removeEventListener("abort", onAbort);
			this.pending.set(String(id), { resolve, reject, cleanup });
			signal?.addEventListener("abort", onAbort, { once: true });
			try {
				this.send({ jsonrpc: "2.0", id, method, params });
			} catch (error) {
				this.pending.delete(String(id));
				cleanup();
				reject(error);
			}
		});
	}

	async listTools() {
		const tools = [];
		let cursor;
		do {
			const result = await this.request("tools/list", cursor ? { cursor } : {});
			if (Array.isArray(result?.tools)) tools.push(...result.tools);
			cursor = result?.nextCursor;
		} while (cursor);
		return tools;
	}

	fail(error) {
		if (this.closed) return;
		this.closed = true;
		for (const pending of this.pending.values()) {
			pending.cleanup();
			pending.reject(error);
		}
		this.pending.clear();
	}

	close() {
		if (this.closed) return;
		this.closed = true;
		try { this.child?.stdin?.end(); } catch {}
		try { this.child?.kill(); } catch {}
		for (const pending of this.pending.values()) {
			pending.cleanup();
			pending.reject(new Error(`${this.server.name}: MCP server closed`));
		}
		this.pending.clear();
	}
}

function safeName(value) {
	return String(value ?? "tool").toLowerCase().replace(/[^a-z0-9_]+/g, "_").replace(/^_+|_+$/g, "") || "tool";
}

function hashName(value) {
	let hash = 2166136261;
	for (const char of value) {
		hash ^= char.charCodeAt(0);
		hash = Math.imul(hash, 16777619);
	}
	return (hash >>> 0).toString(36);
}

function piToolName(serverName, toolName, used) {
	const original = `mcp_${safeName(serverName)}_${safeName(toolName)}`;
	let name = original;
	if (name.length > 64) name = `${name.slice(0, 55)}_${hashName(original)}`;
	let suffix = 2;
	while (used.has(name)) {
		const ending = `_${suffix++}`;
		name = `${original.slice(0, 64 - ending.length)}${ending}`;
	}
	used.add(name);
	return name;
}

function resultContent(result) {
	const content = [];
	for (const item of result?.content ?? []) {
		if (item?.type === "text" && typeof item.text === "string") {
			content.push({ type: "text", text: item.text });
		} else if (item?.type === "image" && typeof item.data === "string" && typeof item.mimeType === "string") {
			content.push({ type: "image", data: item.data, mimeType: item.mimeType });
		} else if (item?.type === "resource" && typeof item.resource?.text === "string") {
			content.push({ type: "text", text: item.resource.text });
		} else if (item !== undefined) {
			content.push({ type: "text", text: JSON.stringify(item) });
		}
	}
	if (content.length === 0 && result?.structuredContent !== undefined) {
		content.push({ type: "text", text: JSON.stringify(result.structuredContent) });
	}
	if (content.length === 0) content.push({ type: "text", text: "MCP tool completed with no content." });
	return content;
}

function shutdown() {
	for (const client of clients) client.close();
}

export default function mcpBridge(pi) {
	let startup;
	pi.on("session_start", async () => {
		if (startup) return startup;
		startup = (async () => {
			try {
				const usedNames = new Set(pi.getAllTools().map((tool) => tool.name));
				for (const server of config.servers) {
					const client = new StdioMCPClient(server);
					clients.push(client);
					await client.start();
					for (const tool of await client.listTools()) {
						if (!tool?.name) continue;
						const name = piToolName(server.name, tool.name, usedNames);
						const description = `[MCP server ${server.name}; tool ${tool.name}] ${tool.description ?? ""}`.trim();
						pi.registerTool({
							name,
							label: `${server.name}: ${tool.name}`,
							description,
							promptSnippet: description,
							parameters: tool.inputSchema ?? { type: "object", properties: {} },
							async execute(_toolCallId, params, signal) {
								const result = await client.request("tools/call", { name: tool.name, arguments: params }, signal);
								const content = resultContent(result);
								if (result?.isError) throw new Error(content.map((item) => item.text ?? "").join("\n") || "MCP tool failed");
								return { content, details: { server: server.name, tool: tool.name, result } };
							},
						});
					}
				}
				writeStatus({ ready: true });
			} catch (error) {
				shutdown();
				writeStatus({ error: errorText(error) });
				throw error;
			}
		})();
		return startup;
	});

	pi.on("session_shutdown", shutdown);
	process.once("exit", shutdown);
}
