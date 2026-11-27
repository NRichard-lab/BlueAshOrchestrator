import { PRODUCT_NAME } from "./constants";

export function visibleProductName(text: string): string {
  return text.replace(/\b(?:Agent Orchestrator|Orchestrator\.inc|AO)\b/g, PRODUCT_NAME);
}
