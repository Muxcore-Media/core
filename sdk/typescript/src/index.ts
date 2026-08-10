import type { ChannelCredentials } from "@grpc/grpc-js";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));

export type ModuleInfo = {
  id?: string;
  name?: string;
  version?: string;
  roles?: string[];
  description?: string;
  author?: string;
  capabilities?: string[];
  dependsOn?: string[];
  minCoreVersion?: string;
  httpAddr?: string;
};

export type Module = {
  info(): ModuleInfo;
  init(): void | Promise<void>;
  start(): void | Promise<void>;
  stop(): void | Promise<void>;
};

export type RunConfig = {
  module: Module;
  grpcAddr?: string;
  moduleId?: string;
  insecure?: boolean;
  tlsCertFile?: string;
  tlsKeyFile?: string;
  tlsCaFile?: string;
};

type RegistrationClient = {
  Register(
    req: Record<string, unknown>,
    cb: (err: Error | null, resp: { accepted?: boolean; error?: string; mesh_addr?: string; node_id?: string }) => void,
  ): void;
  Unregister(
    req: { module_id: string },
    cb: (err: Error | null, resp: unknown) => void,
  ): void;
};

function protoRoot(): string {
  // dist/ -> ../proto ; src/ during vitest via proto path override
  return join(__dirname, "..", "proto");
}

export function loadRegistrationClient(
  addr: string,
  credentials: ChannelCredentials,
): { client: RegistrationClient; close: () => void } {
  const def = protoLoader.loadSync(
    join(protoRoot(), "muxcore/module/v1/registration.proto"),
    {
      keepCase: true,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      includeDirs: [protoRoot()],
    },
  );
  const pkg = grpc.loadPackageDefinition(def) as any;
  const ClientCtor = pkg.muxcore.module.v1.ModuleRegistration;
  const client = new ClientCtor(addr, credentials) as RegistrationClient;
  return {
    client,
    close: () => (client as unknown as grpc.Client).close(),
  };
}

export function dialCredentials(cfg: {
  insecure?: boolean;
  tlsCertFile?: string;
  tlsKeyFile?: string;
  tlsCaFile?: string;
}): ChannelCredentials {
  const envInsecure = (process.env.MUXCORE_INSECURE_DISABLE_TLS || "").toLowerCase();
  if (
    cfg.insecure ||
    envInsecure === "1" ||
    envInsecure === "true" ||
    envInsecure === "yes"
  ) {
    return grpc.credentials.createInsecure();
  }
  const certFile = cfg.tlsCertFile || process.env.MUXCORE_TLS_CERT || "";
  const keyFile = cfg.tlsKeyFile || process.env.MUXCORE_TLS_KEY || "";
  const caFile = cfg.tlsCaFile || process.env.MUXCORE_TLS_CA || "";
  if (!certFile || !keyFile || !caFile) {
    throw new Error(
      "TLS required: set cert/key/ca or MUXCORE_TLS_* / MUXCORE_INSECURE_DISABLE_TLS=true",
    );
  }
  return grpc.credentials.createSsl(
    readFileSync(caFile),
    readFileSync(keyFile),
    readFileSync(certFile),
  );
}

function resolveAddr(explicit?: string): string {
  return (
    explicit ||
    process.env.MUXCORE_GRPC_ADDR ||
    process.env.MUXCORE_MESH_ADDR ||
    ""
  );
}

function parseCliOverrides(): { addr: string; id: string } {
  const argv = process.argv.slice(2);
  let addr = "";
  let id = "";
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--muxcore-mesh-addr" && argv[i + 1]) {
      addr = argv[++i];
    } else if (a.startsWith("--muxcore-mesh-addr=")) {
      addr = a.slice("--muxcore-mesh-addr=".length);
    } else if (a === "--muxcore-module-id" && argv[i + 1]) {
      id = argv[++i];
    } else if (a.startsWith("--muxcore-module-id=")) {
      id = a.slice("--muxcore-module-id=".length);
    }
  }
  return { addr, id };
}

function registerAsync(
  client: RegistrationClient,
  req: Record<string, unknown>,
): Promise<{ accepted?: boolean; error?: string; mesh_addr?: string; node_id?: string }> {
  return new Promise((resolve, reject) => {
    client.Register(req, (err, resp) => (err ? reject(err) : resolve(resp)));
  });
}

function unregisterAsync(client: RegistrationClient, moduleId: string): Promise<void> {
  return new Promise((resolve, reject) => {
    client.Unregister({ module_id: moduleId }, (err) => (err ? reject(err) : resolve()));
  });
}

/** Register with muxcored, run until SIGINT/SIGTERM, then unregister. */
export async function run(cfg: RunConfig): Promise<void> {
  if (!cfg.module) {
    throw new Error("module is required");
  }
  const cli = parseCliOverrides();
  const grpcAddr = resolveAddr(cfg.grpcAddr || cli.addr);
  const info = cfg.module.info();
  const moduleId =
    cfg.moduleId ||
    process.env.MUXCORE_MODULE_ID ||
    cli.id ||
    info.id ||
    "";

  if (!grpcAddr) {
    throw new Error(
      "core gRPC address required — set MUXCORE_GRPC_ADDR/MUXCORE_MESH_ADDR or --muxcore-mesh-addr",
    );
  }
  if (!moduleId) {
    throw new Error("module id required — set MUXCORE_MODULE_ID or --muxcore-module-id");
  }

  const creds = dialCredentials(cfg);
  const { client, close } = loadRegistrationClient(grpcAddr, creds);
  try {
    const resp = await registerAsync(client, {
      module_id: moduleId,
      min_core_version: info.minCoreVersion || "",
      module_info: {
        id: moduleId,
        name: info.name || moduleId,
        version: info.version || "0.0.0",
        roles: info.roles || [],
        description: info.description || "",
        author: info.author || "",
        capabilities: info.capabilities || [],
        depends_on: info.dependsOn || [],
        min_core_version: info.minCoreVersion || "",
        http_addr: info.httpAddr || "",
      },
    });
    if (!resp.accepted) {
      throw new Error(`core rejected registration: ${resp.error || "unknown"}`);
    }
    console.info(
      `registered id=${moduleId} version=${info.version || "0.0.0"} mesh=${resp.mesh_addr || ""} node=${resp.node_id || ""}`,
    );

    await cfg.module.init();
    console.info(`initialized id=${moduleId}`);
    await cfg.module.start();
    console.info(`started id=${moduleId}`);

    await new Promise<void>((resolve) => {
      const stop = () => {
        process.off("SIGINT", stop);
        process.off("SIGTERM", stop);
        resolve();
      };
      process.on("SIGINT", stop);
      process.on("SIGTERM", stop);
    });

    try {
      await cfg.module.stop();
    } catch (err) {
      console.error(`stop failed id=${moduleId}`, err);
    }
    try {
      await unregisterAsync(client, moduleId);
    } catch (err) {
      console.error(`unregister failed id=${moduleId}`, err);
    }
    console.info(`stopped id=${moduleId}`);
  } finally {
    close();
  }
}
