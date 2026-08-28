// NotFoundPage is what every unresolvable address lands on: a mistyped
// slug, a queue that was deleted, a ticket from a link that was edited.
//
// It says the same thing for all of them, on purpose. The API answers a
// missing org and another business's queue identically so the URL space
// cannot be probed for a list of customers, and a front end that
// distinguished them in prose would give away exactly what the API
// withholds.
export function NotFoundPage() {
  return (
    <main className="page page--centred">
      <div className="card card--message">
        <h1>We couldn&rsquo;t find that page</h1>
        <p>
          The link may be mistyped, or the business may have taken it down. If
          you scanned a code, try scanning it again.
        </p>
      </div>
    </main>
  );
}
