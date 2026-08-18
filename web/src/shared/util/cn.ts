// cn() — the standard shadcn className merger. Combines clsx
// (conditional class composition) with tailwind-merge (collision
// resolution: later utility wins). Phase 32.13.
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
