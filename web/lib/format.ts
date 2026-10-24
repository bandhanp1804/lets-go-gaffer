export const one = (n: number): string => n.toFixed(1);
export const two = (n: number): string => n.toFixed(2);
export const three = (n: number): string => n.toFixed(3);
/** +0.4 / -0.8 / 0.0 */
export const signed = (n: number): string => (n > 0 ? `+${n.toFixed(1)}` : n.toFixed(1));

/** Only web addresses become links; anything else (such as fpl://) is not a page. */
export function webURL(url: string): string | undefined {
  return /^https?:\/\//i.test(url) ? url : undefined;
}
