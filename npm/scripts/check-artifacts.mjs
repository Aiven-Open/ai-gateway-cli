// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { inspectArtifacts } from "./artifacts.mjs";

if (process.argv.length !== 3) throw new Error("usage: node scripts/check-artifacts.mjs <artifact-directory>");
process.stdout.write(`${JSON.stringify(inspectArtifacts(process.argv[2]), null, 2)}\n`);
