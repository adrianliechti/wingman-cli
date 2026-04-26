// Maps a filename or extension to a Devicon CSS class. Returns null if no
// devicon matches — callers should fall back to a generic file icon.
//
// Verified against devicon@latest's devicon.min.css. Adding entries is cheap;
// just confirm the class actually exists in node_modules/devicon/devicon.min.css.

const extMap: Record<string, string> = {
	ts: "devicon-typescript-plain",
	tsx: "devicon-typescript-plain",
	js: "devicon-javascript-plain",
	jsx: "devicon-javascript-plain",
	mjs: "devicon-javascript-plain",
	cjs: "devicon-javascript-plain",
	go: "devicon-go-original-wordmark",
	py: "devicon-python-plain",
	rs: "devicon-rust-original",
	java: "devicon-java-plain",
	rb: "devicon-ruby-plain",
	php: "devicon-php-plain",
	c: "devicon-c-plain",
	h: "devicon-c-plain",
	cpp: "devicon-cplusplus-plain",
	hpp: "devicon-cplusplus-plain",
	cc: "devicon-cplusplus-plain",
	cs: "devicon-csharp-plain",
	swift: "devicon-swift-plain",
	kt: "devicon-kotlin-plain",
	kts: "devicon-kotlin-plain",
	sh: "devicon-bash-plain",
	bash: "devicon-bash-plain",
	zsh: "devicon-bash-plain",
	yaml: "devicon-yaml-plain",
	yml: "devicon-yaml-plain",
	json: "devicon-json-plain",
	html: "devicon-html5-plain",
	htm: "devicon-html5-plain",
	css: "devicon-css3-plain",
	scss: "devicon-sass-plain",
	sass: "devicon-sass-plain",
	vue: "devicon-vuejs-plain",
	svelte: "devicon-svelte-plain",
	md: "devicon-markdown-original",
	markdown: "devicon-markdown-original",
	lua: "devicon-lua-plain",
	dart: "devicon-dart-plain",
	ex: "devicon-elixir-plain",
	exs: "devicon-elixir-plain",
	erl: "devicon-erlang-plain",
	hrl: "devicon-erlang-plain",
	hs: "devicon-haskell-plain",
	ml: "devicon-ocaml-plain",
	mli: "devicon-ocaml-plain",
	zig: "devicon-zig-original",
};

const nameMap: Record<string, string> = {
	dockerfile: "devicon-docker-plain",
	"docker-compose.yml": "devicon-docker-plain",
	"docker-compose.yaml": "devicon-docker-plain",
	"package.json": "devicon-nodejs-plain",
	"package-lock.json": "devicon-nodejs-plain",
	"tsconfig.json": "devicon-typescript-plain",
	".gitignore": "devicon-git-plain",
	".gitattributes": "devicon-git-plain",
	".gitmodules": "devicon-git-plain",
	"go.mod": "devicon-go-original-wordmark",
	"go.sum": "devicon-go-original-wordmark",
	"cargo.toml": "devicon-rust-original",
	"cargo.lock": "devicon-rust-original",
	makefile: "devicon-cmake-plain",
	"cmakelists.txt": "devicon-cmake-plain",
};

// getDeviconClass returns the devicon class for a given file name, or null
// if no mapping exists. By default the icon inherits the surrounding text
// color (`currentColor`); pass `colored: true` to use the icon's brand color.
export function getDeviconClass(
	filename: string,
	colored = false,
): string | null {
	const lower = filename.toLowerCase();
	const suffix = colored ? " colored" : "";
	if (nameMap[lower]) return `${nameMap[lower]}${suffix}`;
	const dot = lower.lastIndexOf(".");
	if (dot < 0) return null;
	const ext = lower.slice(dot + 1);
	if (extMap[ext]) return `${extMap[ext]}${suffix}`;
	return null;
}
