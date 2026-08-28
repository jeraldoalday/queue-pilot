/**
 * The line as numbers, which is the only form it is ever shown in
 * publicly. A whole room reads a board, so it carries no names — the API
 * does not send them here, and this component could not display one if it
 * wanted to.
 *
 * `mine` highlights the reader's own number. It is the difference between
 * a customer holding 44 seeing a list and seeing themselves in it.
 */
export function NumberList({
  numbers,
  mine,
  emptyText = "Nobody is waiting",
}: {
  numbers: number[];
  mine?: number;
  emptyText?: string;
}) {
  if (numbers.length === 0) {
    return <p className="muted small">{emptyText}</p>;
  }

  return (
    <ul className="number-list">
      {numbers.map((n) => (
        <li key={n} className={n === mine ? "is-mine" : undefined}>
          {n}
          {n === mine ? <span className="visually-hidden"> (your number)</span> : null}
        </li>
      ))}
    </ul>
  );
}
