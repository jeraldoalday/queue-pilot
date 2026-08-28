export interface Stat {
  name: string;
  value: number | string;
}

/**
 * The counts under a board. A grid rather than a sentence because the
 * numbers are compared against each other — "how long is this line" is
 * answered by reading two of them together.
 */
export function StatGrid({ stats }: { stats: Stat[] }) {
  return (
    <div className="stat-grid">
      {stats.map((stat) => (
        <div className="stat" key={stat.name}>
          <span className="value">{stat.value}</span>
          <span className="name">{stat.name}</span>
        </div>
      ))}
    </div>
  );
}
