// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

export const targets = [
  { os: "linux", cpu: "x64", goos: "linux", goarch: "amd64" },
  { os: "linux", cpu: "arm64", goos: "linux", goarch: "arm64" },
  { os: "linux", cpu: "arm", goos: "linux", goarch: "arm", goarm: "7" },
  { os: "darwin", cpu: "x64", goos: "darwin", goarch: "amd64" },
  { os: "darwin", cpu: "arm64", goos: "darwin", goarch: "arm64" },
  { os: "win32", cpu: "x64", goos: "windows", goarch: "amd64" },
  { os: "win32", cpu: "arm64", goos: "windows", goarch: "arm64" },
];

export const packageName = ({ os, cpu }) => `@aiven/ai-gateway-${os}-${cpu}`;
export const binaryName = ({ os }) => `aiven-ai-gateway${os === "win32" ? ".exe" : ""}`;

export function selectTarget(os, cpu, armVersion) {
  const target = targets.find((t) => t.os === os && t.cpu === cpu);
  if (!target || (cpu === "arm" && !(Number(armVersion) >= 7))) {
    throw new Error("unsupported platform; supported: Linux x64/ARM64/ARMv7, macOS x64/ARM64, Windows x64/ARM64");
  }
  return target;
}
