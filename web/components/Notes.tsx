export function Notes({ notes }: { notes?: string[] }) {
  if (!notes || notes.length === 0) return null;
  return (
    <ul className="notes">
      {notes.map((n) => (
        <li key={n}>{n}</li>
      ))}
    </ul>
  );
}
