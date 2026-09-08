import base from "./vite.config";
export default {
	...base,
	server: { proxy: { "/api": { target: "http://127.0.0.1:4343", ws: true } } },
};
