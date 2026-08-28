import type { ReactNode } from "react";

/**
 * A short message above the content it is about — a validation failure, a
 * queue that has closed, a connection that dropped.
 *
 * Errors are given role="alert" so a screen reader announces them when
 * they appear. Everything else is not: a polling page re-renders its
 * notices every fifteen seconds, and announcing "4 people waiting" four
 * times a minute makes the page unusable for exactly the person the
 * announcement was meant to help.
 */
export function Notice({
  tone = "info",
  children,
}: {
  tone?: "info" | "error";
  children: ReactNode;
}) {
  return (
    <p
      className={tone === "error" ? "notice notice--error" : "notice"}
      {...(tone === "error" ? { role: "alert" } : {})}
    >
      {children}
    </p>
  );
}
