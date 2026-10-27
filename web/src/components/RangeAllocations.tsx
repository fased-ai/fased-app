export function RangeAllocations({
  label,
  amounts,
}: {
  label: string;
  amounts: string[];
}) {
  return (
    <table aria-label={label}>
      <thead>
        <tr>
          <th>Range</th>
          <th>SOL</th>
          <th>Lamports</th>
        </tr>
      </thead>
      <tbody>
        {amounts.map((value, i) => {
          const n = BigInt(value);
          const sol = `${n / 1000000000n}.${(n % 1000000000n).toString().padStart(9, "0")}`;
          return (
            <tr key={i}>
              <td>{String.fromCharCode(65 + i)}</td>
              <td>{sol}</td>
              <td>{value}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
