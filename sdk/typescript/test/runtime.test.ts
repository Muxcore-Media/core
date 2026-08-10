import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { run, type Module } from "../src/index.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const protoRoot = join(__dirname, "..", "proto");

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createServer();
    s.listen(0, "127.0.0.1", () => {
      const addr = s.address();
      if (!addr || typeof addr === "string") {
        reject(new Error("no port"));
        return;
      }
      const port = addr.port;
      s.close(() => resolve(port));
    });
  });
}

describe("run lifecycle", () => {
  let server: grpc.Server;
  let addr: string;
  const registered: string[] = [];
  const unregistered: string[] = [];

  beforeAll(async () => {
    const port = await freePort();
    addr = `127.0.0.1:${port}`;
    const def = protoLoader.loadSync(
      join(protoRoot, "muxcore/module/v1/registration.proto"),
      {
        keepCase: true,
        longs: String,
        enums: String,
        defaults: true,
        oneofs: true,
        includeDirs: [protoRoot],
      },
    );
    const pkg = grpc.loadPackageDefinition(def) as any;
    server = new grpc.Server();
    server.addService(pkg.muxcore.module.v1.ModuleRegistration.service, {
      Register: (
        call: { request: { module_id: string } },
        cb: (err: Error | null, resp: object) => void,
      ) => {
        registered.push(call.request.module_id);
        cb(null, { accepted: true, mesh_addr: addr, node_id: "node-1" });
      },
      Unregister: (
        call: { request: { module_id: string } },
        cb: (err: Error | null, resp: object) => void,
      ) => {
        unregistered.push(call.request.module_id);
        cb(null, { acknowledged: true });
      },
      BootstrapRegister: (
        _call: unknown,
        cb: (err: Error | null, resp: object) => void,
      ) => {
        cb(null, { accepted: false, error: "unused" });
      },
    });
    await new Promise<void>((resolve, reject) => {
      server.bindAsync(addr, grpc.ServerCredentials.createInsecure(), (err) =>
        err ? reject(err) : resolve(),
      );
    });
  });

  afterAll(async () => {
    await new Promise<void>((resolve) => server.tryShutdown(() => resolve()));
  });

  it("registers and unregisters on SIGTERM", async () => {
    registered.length = 0;
    unregistered.length = 0;

    const mod: Module = {
      info: () => ({ id: "demo-ts", name: "demo-ts", version: "0.1.0", roles: ["tool"] }),
      init: () => undefined,
      start: () => {
        setTimeout(() => process.emit("SIGTERM"), 50);
      },
      stop: () => undefined,
    };

    await run({ module: mod, grpcAddr: addr, insecure: true });
    expect(registered).toEqual(["demo-ts"]);
    expect(unregistered).toEqual(["demo-ts"]);
  });
});
