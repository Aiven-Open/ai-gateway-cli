// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { compareBuilds } from "./reproducibility.mjs";

if (process.argv.length !== 4) throw new Error("usage: node scripts/check-reproducibility.mjs <first-build> <second-build>");
process.stdout.write(`${JSON.stringify(compareBuilds(process.argv[2], process.argv[3]), null, 2)}\n`);
