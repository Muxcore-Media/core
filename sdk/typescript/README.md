# MuxCore TypeScript SDK

Scaffold for language-agnostic module SDKs (§4.1) — **TypeScript** `v0.1.0` (alongside Python / Go).

Uses `@grpc/grpc-js` + `@grpc/proto-loader` against a vendored copy of `ModuleRegistration` proto.

## Install

```bash
cd core/sdk/typescript
npm install
npm run build
```

## Quick start

```ts
import { run, type Module } from "@muxcore-media/sdk";

const mod: Module = {
  info: () => ({
    id: "echo-ts",
    name: "echo-ts",
    version: "0.1.0",
    roles: ["tool"],
    capabilities: ["demo"],
  }),
  init: () => {},
  start: () => {},
  stop: () => {},
};

await run({ module: mod, insecure: true });
```

```bash
MUXCORE_GRPC_ADDR=127.0.0.1:9090 MUXCORE_INSECURE_DISABLE_TLS=true node dist/example.js
```

## Test

```bash
npm test
```

## Out of scope

- Discovery / Storage / Events client parity with Go
- Settings mesh
- Rust SDK
