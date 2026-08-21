import {
	createColumnHelper,
	createSortedRowModel,
	rowSortingFeature,
	sortFn_alphanumeric,
	tableFeatures,
	useTable,
} from "@tanstack/react-table";
import {
	ArrowDown,
	ArrowUp,
	ChevronRight,
	ChevronsUpDown,
	ListTree,
	Waypoints,
} from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { parse as parseToml } from "smol-toml";
import { parse as parseYaml } from "yaml";
import {
	collectionEntries,
	collectionSummary,
	formatScalar,
	scalarClass,
} from "../utils/dataValue";
import { DataGraph } from "./DataGraph";

type DataFormat = "json" | "yaml" | "toml" | "xml" | "csv" | "tsv";
type StructuredView = "tree" | "graph";

const MAX_TABLE_ROWS = 1_000;
const MAX_TABLE_COLUMNS = 100;
const MAX_TREE_CHILDREN = 500;

const DATA_TABLE_FEATURES = tableFeatures({
	rowSortingFeature,
	sortedRowModel: createSortedRowModel(),
	sortFns: { alphanumeric: sortFn_alphanumeric },
});
const dataColumnHelper = createColumnHelper<
	typeof DATA_TABLE_FEATURES,
	string[]
>();

export function DataPreview({
	text,
	format,
	path,
}: {
	text: string;
	format: DataFormat;
	path: string;
}) {
	const [view, setView] = useState<StructuredView>("tree");
	const result = useMemo(() => {
		try {
			if (format === "csv" || format === "tsv") {
				return {
					value: parseDelimited(text, format === "csv" ? "," : "\t"),
				};
			}
			return {
				value:
					format === "json"
						? JSON.parse(text)
						: format === "toml"
							? parseToml(text)
							: format === "xml"
								? parseXml(text)
								: parseYaml(text, { maxAliasCount: 100 }),
			};
		} catch (error) {
			return {
				error: error instanceof Error ? error.message : String(error),
			};
		}
	}, [format, text]);

	if (result.error) {
		return (
			<div
				role="alert"
				className="m-6 rounded-md border border-danger/30 bg-danger/5 p-4 text-[12px] text-danger"
			>
				Could not preview {path}: {result.error}
			</div>
		);
	}

	if (format === "csv" || format === "tsv") {
		return (
			<TablePreview
				rows={(result.value as DelimitedResult).rows}
				truncated={(result.value as DelimitedResult).truncated}
				path={path}
			/>
		);
	}

	return (
		<div
			aria-label={`Preview of ${path}`}
			className="relative h-full min-h-0 overflow-hidden"
		>
			{view === "graph" ? (
				<DataGraph value={result.value} />
			) : (
				<div
					data-structured-preview
					className="h-full overflow-auto px-5 py-4 font-mono text-[12px] select-text"
				>
					<DataNode value={result.value} depth={0} ancestors={[]} />
				</div>
			)}
			<div className="absolute bottom-3 left-3 z-10 flex gap-0.5 rounded-md border border-border bg-bg-elevated/95 p-0.5 shadow-sm">
				<ViewToggleButton
					label="Tree"
					icon={<ListTree size={12} />}
					active={view === "tree"}
					onClick={() => setView("tree")}
				/>
				<ViewToggleButton
					label="Graph"
					icon={<Waypoints size={12} />}
					active={view === "graph"}
					onClick={() => setView("graph")}
				/>
			</div>
		</div>
	);
}

function ViewToggleButton({
	label,
	icon,
	active,
	onClick,
}: {
	label: string;
	icon: ReactNode;
	active: boolean;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			aria-pressed={active}
			onClick={onClick}
			className={`flex h-6 items-center gap-1.5 rounded px-2 text-[11px] ${
				active ? "bg-bg-active text-fg" : "text-fg-dim hover:text-fg"
			}`}
		>
			{icon}
			{label}
		</button>
	);
}

function parseXml(text: string): unknown {
	const document = new DOMParser().parseFromString(text, "application/xml");
	const parseError = document.querySelector("parsererror");
	if (parseError) {
		throw new Error(parseError.textContent?.trim() || "Invalid XML document");
	}
	if (!document.documentElement) throw new Error("Empty XML document");
	return {
		[document.documentElement.tagName]: xmlElementValue(
			document.documentElement,
		),
	};
}

function xmlElementValue(element: Element): unknown {
	const value: Record<string, unknown> = {};
	for (const attribute of element.attributes) {
		value[`@${attribute.name}`] = attribute.value;
	}

	const childElements = Array.from(element.children);
	const text = Array.from(element.childNodes)
		.filter(
			(node) =>
				node.nodeType === Node.TEXT_NODE ||
				node.nodeType === Node.CDATA_SECTION_NODE,
		)
		.map((node) => node.textContent ?? "")
		.join("")
		.trim();
	if (text) value["#text"] = text;

	for (const child of childElements) {
		const childValue = xmlElementValue(child);
		const existing = value[child.tagName];
		if (existing === undefined) value[child.tagName] = childValue;
		else if (Array.isArray(existing)) existing.push(childValue);
		else value[child.tagName] = [existing, childValue];
	}

	const keys = Object.keys(value);
	if (keys.length === 0) return "";
	if (keys.length === 1 && keys[0] === "#text") return value["#text"];
	return value;
}

function DataNode({
	name,
	value,
	depth,
	ancestors,
}: {
	name?: string;
	value: unknown;
	depth: number;
	ancestors: object[];
}) {
	const [open, setOpen] = useState(depth < 2);
	const collection = value !== null && typeof value === "object";
	const circular = collection && ancestors.includes(value);
	const entries = circular ? [] : collectionEntries(value);
	const expandable = entries.length > 0;
	const paddingLeft = depth * 16;

	if (!expandable) {
		return (
			<div
				className="flex min-h-6 items-start gap-2 py-0.5"
				style={{ paddingLeft }}
			>
				{name !== undefined && <DataKey name={name} />}
				<span className={scalarClass(value)}>
					{circular ? "[Circular]" : formatScalar(value)}
				</span>
			</div>
		);
	}

	const visibleEntries = entries.slice(0, MAX_TREE_CHILDREN);
	const nextAncestors = collection ? [...ancestors, value] : ancestors;
	return (
		<div>
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
				className="flex min-h-6 w-full items-center gap-1 rounded py-0.5 text-left hover:bg-bg-hover"
				style={{ paddingLeft }}
			>
				<ChevronRight
					size={12}
					className={`shrink-0 text-fg-dim transition-transform ${open ? "rotate-90" : ""}`}
				/>
				{name !== undefined && <DataKey name={name} />}
				<span className="text-fg-dim">{collectionSummary(value)}</span>
			</button>
			{open && (
				<div>
					{visibleEntries.map(([key, child], index) => (
						<DataNode
							key={`${key}:${index}`}
							name={key}
							value={child}
							depth={depth + 1}
							ancestors={nextAncestors}
						/>
					))}
					{entries.length > visibleEntries.length && (
						<div
							className="py-1 text-fg-dim"
							style={{ paddingLeft: (depth + 1) * 16 }}
						>
							… {entries.length - visibleEntries.length} more entries
						</div>
					)}
				</div>
			)}
		</div>
	);
}

function DataKey({ name }: { name: string }) {
	return <span className="shrink-0 text-accent">{name}:</span>;
}

interface DelimitedResult {
	rows: string[][];
	truncated: boolean;
}

function parseDelimited(text: string, delimiter: string): DelimitedResult {
	if (!text) return { rows: [], truncated: false };

	const rows: string[][] = [];
	let row: string[] = [];
	let field = "";
	let quoted = false;
	let truncated = false;

	const finishRow = () => {
		row.push(field);
		if (rows.length < MAX_TABLE_ROWS) rows.push(row);
		else truncated = true;
		row = [];
		field = "";
	};

	for (let index = 0; index < text.length; index += 1) {
		const character = text[index];
		if (quoted) {
			if (character === '"' && text[index + 1] === '"') {
				field += '"';
				index += 1;
			} else if (character === '"') {
				quoted = false;
			} else {
				field += character;
			}
			continue;
		}
		if (character === '"' && field === "") {
			quoted = true;
		} else if (character === delimiter) {
			row.push(field);
			field = "";
		} else if (character === "\n") {
			finishRow();
		} else if (character !== "\r") {
			field += character;
		}
	}

	if (field !== "" || row.length > 0) finishRow();
	return { rows, truncated };
}

function TablePreview({
	rows,
	truncated,
	path,
}: {
	rows: string[][];
	truncated: boolean;
	path: string;
}) {
	const headers = useMemo(() => {
		if (rows.length === 0) return [];
		const columnCount = Math.min(
			MAX_TABLE_COLUMNS,
			Math.max(...rows.map((row) => row.length)),
		);
		return Array.from(
			{ length: columnCount },
			(_, index) => rows[0][index] || `Column ${index + 1}`,
		);
	}, [rows]);
	const data = useMemo(() => rows.slice(1), [rows]);
	const columns = useMemo(
		() =>
			dataColumnHelper.columns(
				headers.map((header, index) =>
					dataColumnHelper.accessor((row) => row[index] ?? "", {
						id: String(index),
						header,
						sortFn: "alphanumeric",
					}),
				),
			),
		[headers],
	);
	const table = useTable({
		features: DATA_TABLE_FEATURES,
		data,
		columns,
	});

	if (rows.length === 0) {
		return (
			<div className="flex h-full items-center justify-center text-[12px] text-fg-dim">
				Empty table
			</div>
		);
	}

	return (
		<div
			data-table-preview
			aria-label={`Preview of ${path}`}
			className="h-full overflow-auto select-text"
		>
			<table className="min-w-full border-separate border-spacing-0 font-mono text-[12px]">
				<thead className="sticky top-0 z-10 bg-bg-surface text-fg">
					{table.getHeaderGroups().map((headerGroup) => (
						<tr key={headerGroup.id}>
							{headerGroup.headers.map((header) => (
								<th
									key={header.id}
									className="whitespace-nowrap border-r border-b border-border px-3 py-2 text-left font-semibold"
									aria-sort={
										header.column.getIsSorted() === "asc"
											? "ascending"
											: header.column.getIsSorted() === "desc"
												? "descending"
												: "none"
									}
								>
									{!header.isPlaceholder && (
										<button
											type="button"
											onClick={header.column.getToggleSortingHandler()}
											className="flex w-full items-center gap-1.5 rounded text-left hover:text-fg"
										>
											<table.FlexRender header={header} />
											{header.column.getIsSorted() === "asc" ? (
												<ArrowUp size={11} aria-hidden="true" />
											) : header.column.getIsSorted() === "desc" ? (
												<ArrowDown size={11} aria-hidden="true" />
											) : (
												<ChevronsUpDown
													size={11}
													aria-hidden="true"
													className="text-fg-dim"
												/>
											)}
										</button>
									)}
								</th>
							))}
						</tr>
					))}
				</thead>
				<tbody className="text-fg-muted">
					{table.getRowModel().rows.map((row) => (
						<tr key={row.id} className="odd:bg-bg-surface/30">
							{row.getAllCells().map((cell) => (
								<td
									key={cell.id}
									className="max-w-96 whitespace-pre-wrap border-r border-b border-border-subtle px-3 py-1.5 align-top"
								>
									<table.FlexRender cell={cell} />
								</td>
							))}
						</tr>
					))}
				</tbody>
			</table>
			{(truncated || rows.some((row) => row.length > MAX_TABLE_COLUMNS)) && (
				<div className="sticky bottom-0 border-t border-warning/30 bg-bg-surface px-3 py-2 text-[11px] text-warning">
					Preview limited to {MAX_TABLE_ROWS.toLocaleString()} rows and{" "}
					{MAX_TABLE_COLUMNS} columns.
				</div>
			)}
		</div>
	);
}
