import { COMMIT_MESSAGES_DIR, treeRealPathOrder, DiffFileTreeEntry } from "./DiffFileTree";

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`Assertion failed: ${msg}`);
}

function run(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`\u2713 ${name}`);
  } catch (err) {
    console.error(`\u2717 ${name}`);
    throw err;
  }
}

function fileEntry(path: string): DiffFileTreeEntry {
  return { realPath: path, treePath: path.split("/"), status: "modified" };
}

function commitEntry(realPath: string, leaf: string): DiffFileTreeEntry {
  return { realPath, treePath: [COMMIT_MESSAGES_DIR, leaf] };
}

run("commit messages folder always sorts first", () => {
  // "Apple" would sort before "Commit messages" alphabetically; the
  // synthetic folder must still lead.
  const entries: DiffFileTreeEntry[] = [
    fileEntry("apple/a.ts"),
    fileEntry("shelley/b.ts"),
    commitEntry("__commitmsg__/deadbeef", "Some commit subject"),
  ];
  const order = treeRealPathOrder(entries);
  assert(order[0] === "__commitmsg__/deadbeef", `commit first: ${order.join(",")}`);
});

run("tree order is directories-depth-first, alphabetical", () => {
  const entries: DiffFileTreeEntry[] = [
    fileEntry("shelley/version/version.go"),
    fileEntry("shelley/server/handlers.go"),
    fileEntry("shelley/server/handlers_test.go"),
    fileEntry("shelley/styles.css"),
  ];
  const order = treeRealPathOrder(entries);
  // Within shelley/: directories (server/, version/) sort before the
  // styles.css file, alphabetically among themselves. Inside server/,
  // localeCompare orders `handlers_test.go` before `handlers.go` (the
  // same order the sidebar tree shows), and navigation must match it.
  assert(
    order.join(",") ===
      [
        "shelley/server/handlers_test.go",
        "shelley/server/handlers.go",
        "shelley/version/version.go",
        "shelley/styles.css",
      ].join(","),
    `order: ${order.join(",")}`,
  );
});

run("navigation order matches a mixed commit-message + files set", () => {
  const entries: DiffFileTreeEntry[] = [
    fileEntry("shelley/server/handlers.go"),
    commitEntry("__commitmsg__/abc", "tighten thing"),
    fileEntry("apple/z.ts"),
  ];
  const order = treeRealPathOrder(entries);
  assert(
    order.join(",") === ["__commitmsg__/abc", "apple/z.ts", "shelley/server/handlers.go"].join(","),
    `order: ${order.join(",")}`,
  );
});

console.log("\nDiffFileTree tests passed");
