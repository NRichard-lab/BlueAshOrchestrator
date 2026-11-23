import { defineConfig } from "vite";
import { brandDefine } from "./vite.brand";

// Forge's VitePlugin handles all preload script build configuration.
export default defineConfig({ define: brandDefine() });
