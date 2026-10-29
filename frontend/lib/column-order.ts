/**
 * The moves ("put id right after `after`, or first if null") that turn the
 * order `current` into `desired`; both hold the same ids. Each move is one
 * POST /columns/{id}/move, so it makes as few as it can for a few columns
 * dragged around: none when nothing changed.
 */
export function columnMoves(current: string[], desired: string[]): { id: string; after: string | null }[] {
  const order = [...current];
  const moves: { id: string; after: string | null }[] = [];
  desired.forEach((id, i) => {
    if (order[i] === id) return;
    moves.push({ id, after: i === 0 ? null : desired[i - 1] });
    order.splice(order.indexOf(id), 1);
    order.splice(i, 0, id);
  });
  return moves;
}
