import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

/**
 * Renders a page at a URL, with the route pattern that supplies its
 * params.
 *
 * Pages read orgSlug and queueSlug from useParams, so rendering one
 * directly gives it empty strings and tests a page the app never shows.
 * Going through the router is also what makes a navigation assertion
 * possible: a test can render the destination route too and check the page
 * actually arrived there.
 */
export function renderAt(
  ui: ReactElement,
  { path, url, extra }: { path: string; url: string; extra?: ReactElement },
) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path={path} element={ui} />
        {extra}
      </Routes>
    </MemoryRouter>,
  );
}
