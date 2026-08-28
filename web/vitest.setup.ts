import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library mounts into a container it appends to document.body and
// does not remove on its own when globals are enabled. Without this, the
// second test in a file queries a DOM containing the first test's render
// as well, and `getByRole` starts failing with "found multiple elements"
// for reasons that have nothing to do with the component under test.
afterEach(() => {
  cleanup();
});
