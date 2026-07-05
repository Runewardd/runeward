/**
 * @runeward/sdk — TypeScript client and Vercel AI SDK tools for the runeward
 * governed execution cell.
 *
 * The core {@link RunewardClient} has no runtime dependencies (uses global
 * `fetch`). The AI SDK tool wrappers in `./ai-tools` require the optional peer
 * dependencies `ai` and `zod`; the LangChain.js wrappers in `./langchain-tools`
 * require `@langchain/core` and `zod`; the Strands wrappers in `./strands-tools`
 * require `@strands-agents/sdk` and `zod`.
 */

export {
  RunewardClient,
  RunewardError,
  RunewardDenied,
  RunewardApprovalRequired,
} from "./client.js";

export type {
  RunewardClientOptions,
  Sandbox,
  Profile,
  ExecResult,
  Approval,
} from "./client.js";

export { makeRunewardTools } from "./ai-tools.js";
