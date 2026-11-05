// Sample rows for the items pages, which show the navigation conventions
// (docs/lidza-guide.md, "Navigation"). In an app they come from a query
// (api.listItems() in a page script); replace these pages with the app's own.

export const categories = ['lighting', 'seating', 'storage'] as const
export type Category = (typeof categories)[number]

export interface Item {
  /** The item's address: /items/<slug>. Lowercase letters, digits and hyphens. */
  slug: string
  name: string
  category: Category
  summary: string
  specs: { label: string; value: string }[]
}

export const items: Item[] = [
  { slug: 'desk-lamp', name: 'Desk lamp', category: 'lighting', summary: 'An adjustable lamp for one desk.', specs: [{ label: 'Height', value: '45 cm' }, { label: 'Bulb', value: 'E27' }] },
  { slug: 'floor-lamp', name: 'Floor lamp', category: 'lighting', summary: 'A tall lamp that lights a corner.', specs: [{ label: 'Height', value: '160 cm' }, { label: 'Bulb', value: 'E27' }] },
  { slug: 'oak-chair', name: 'Oak chair', category: 'seating', summary: 'A chair in solid oak.', specs: [{ label: 'Seat height', value: '46 cm' }, { label: 'Wood', value: 'Oak' }] },
  { slug: 'low-stool', name: 'Low stool', category: 'seating', summary: 'A stool that stacks.', specs: [{ label: 'Seat height', value: '30 cm' }, { label: 'Stacks', value: 'Up to 6' }] },
  { slug: 'wall-shelf', name: 'Wall shelf', category: 'storage', summary: 'One shelf on two brackets.', specs: [{ label: 'Width', value: '80 cm' }, { label: 'Load', value: '15 kg' }] },
]

export function findItem(slug: string): Item | undefined {
  return items.find((item) => item.slug === slug)
}
