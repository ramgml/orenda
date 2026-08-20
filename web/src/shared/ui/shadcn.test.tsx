// @vitest-environment jsdom
/**
 * shadcn/ui primitives tests (Phase 32.13, task 12 step 2).
 *
 * Two contracts are pinned here:
 *
 *  1. Every primitive added through `npx shadcn add` renders and carries
 *     the shadcn token classes (bg-primary, border-input, bg-popover, …)
 *     rather than hardcoded palette classes.
 *  2. Those token classes actually resolve through Tailwind against the
 *     CSS variables declared in src/index.css, in BOTH themes: the
 *     compiled stylesheet must emit `hsl(var(--token))` utilities and
 *     index.css must define the token in `:root` and in `.dark`.
 *
 * (2) is the light/dark smoke check — no pixel comparison, just proof
 * that the variable plumbing is complete on both sides.
 */
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { cleanup, render, screen } from '@testing-library/react';
import postcss from 'postcss';
import tailwindcss from 'tailwindcss';
import { afterEach, describe, expect, it } from 'vitest';

import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/ui/card';
import { DropdownMenu, DropdownMenuTrigger } from '@/shared/ui/dropdown-menu';
import { Input } from '@/shared/ui/input';
import { Popover, PopoverTrigger } from '@/shared/ui/popover';
import { Select, SelectTrigger, SelectValue } from '@/shared/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';
import { Textarea } from '@/shared/ui/textarea';
import { Tooltip, TooltipProvider, TooltipTrigger } from '@/shared/ui/tooltip';

afterEach(() => {
  cleanup();
});

describe('Button', () => {
  it('renders the default variant with primary tokens', () => {
    render(<Button>Save</Button>);
    const el = screen.getByRole('button', { name: 'Save' });
    expect(el.className).toContain('bg-primary');
    expect(el.className).toContain('text-primary-foreground');
  });

  it('applies variant and size classes', () => {
    render(
      <Button variant="destructive" size="sm">
        Delete
      </Button>,
    );
    const el = screen.getByRole('button', { name: 'Delete' });
    expect(el.className).toContain('bg-destructive');
    expect(el.className).toContain('h-9');
  });

  it('renders as a child element with asChild', () => {
    render(
      <Button asChild>
        <a href="/tasks">Tasks</a>
      </Button>,
    );
    const el = screen.getByRole('link', { name: 'Tasks' });
    expect(el.tagName).toBe('A');
    expect(el.className).toContain('bg-primary');
  });
});

describe('form primitives', () => {
  it('Input renders with the input border token', () => {
    render(<Input placeholder="Title" />);
    const el = screen.getByPlaceholderText('Title');
    expect(el.tagName).toBe('INPUT');
    expect(el.className).toContain('border-input');
  });

  it('Textarea renders with the input border token', () => {
    render(<Textarea placeholder="Notes" />);
    const el = screen.getByPlaceholderText('Notes');
    expect(el.tagName).toBe('TEXTAREA');
    expect(el.className).toContain('border-input');
  });

  it('Select renders a combobox trigger', () => {
    render(
      <Select>
        <SelectTrigger aria-label="Status">
          <SelectValue placeholder="Pick one" />
        </SelectTrigger>
      </Select>,
    );
    const el = screen.getByRole('combobox', { name: 'Status' });
    expect(el.className).toContain('border-input');
  });
});

describe('overlay primitives', () => {
  it('DropdownMenu renders a closed trigger', () => {
    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Menu</DropdownMenuTrigger>
      </DropdownMenu>,
    );
    const el = screen.getByRole('button', { name: 'Menu' });
    expect(el.getAttribute('data-state')).toBe('closed');
  });

  it('Popover renders a closed trigger', () => {
    render(
      <Popover>
        <PopoverTrigger>Details</PopoverTrigger>
      </Popover>,
    );
    expect(screen.getByRole('button', { name: 'Details' }).getAttribute('data-state')).toBe(
      'closed',
    );
  });

  it('Tooltip renders its trigger inside a provider', () => {
    render(
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger>Help</TooltipTrigger>
        </Tooltip>
      </TooltipProvider>,
    );
    expect(screen.getByText('Help')).toBeTruthy();
  });
});

describe('layout primitives', () => {
  it('Tabs shows the active panel only', () => {
    render(
      <Tabs defaultValue="a">
        <TabsList>
          <TabsTrigger value="a">First</TabsTrigger>
          <TabsTrigger value="b">Second</TabsTrigger>
        </TabsList>
        <TabsContent value="a">Panel A</TabsContent>
        <TabsContent value="b">Panel B</TabsContent>
      </Tabs>,
    );
    expect(screen.getByText('Panel A')).toBeTruthy();
    expect(screen.queryByText('Panel B')).toBeNull();
    expect(screen.getByRole('tab', { name: 'First' }).getAttribute('data-state')).toBe('active');
  });

  it('Card renders header, title, description and content', () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Backups</CardTitle>
          <CardDescription>Nightly snapshots</CardDescription>
        </CardHeader>
        <CardContent>3 archives</CardContent>
      </Card>,
    );
    expect(screen.getByText('Backups')).toBeTruthy();
    expect(screen.getByText('Nightly snapshots').className).toContain('text-muted-foreground');
    expect(screen.getByText('3 archives')).toBeTruthy();
  });

  it('Badge applies its variant tokens', () => {
    render(<Badge variant="secondary">beta</Badge>);
    expect(screen.getByText('beta').className).toContain('bg-secondary');
  });
});

describe('theme tokens', () => {
  const tokens = [
    'background',
    'foreground',
    'primary',
    'primary-foreground',
    'secondary',
    'secondary-foreground',
    'muted',
    'muted-foreground',
    'accent',
    'accent-foreground',
    'destructive',
    'destructive-foreground',
    'popover',
    'popover-foreground',
    'card',
    'card-foreground',
    'border',
    'input',
    'ring',
  ];

  const indexCss = readFileSync(resolve(__dirname, '../../index.css'), 'utf8');
  const block = (selector: string): string => {
    const start = indexCss.indexOf(`${selector} {`);
    expect(start, `${selector} block missing from index.css`).toBeGreaterThan(-1);
    return indexCss.slice(start, indexCss.indexOf('\n  }', start));
  };

  it('declares every token in both :root and .dark', () => {
    const light = block(':root');
    const dark = block('.dark');
    for (const token of tokens) {
      expect(light, `--${token} missing in :root`).toContain(`--${token}:`);
      expect(dark, `--${token} missing in .dark`).toContain(`--${token}:`);
    }
  });

  it('compiles token utilities against the CSS variables', async () => {
    const config = {
      presets: [(await import('../../../tailwind.config.js')).default],
      content: [
        {
          raw: 'bg-primary text-primary-foreground bg-popover border-input ring-ring bg-card text-muted-foreground bg-background rounded-md',
          extension: 'html',
        },
      ],
    };
    const result = await postcss([tailwindcss(config)]).process('@tailwind utilities;', {
      from: undefined,
    });

    expect(result.css).toContain('hsl(var(--primary))');
    expect(result.css).toContain('hsl(var(--popover))');
    expect(result.css).toContain('hsl(var(--input))');
    expect(result.css).toContain('hsl(var(--ring))');
    expect(result.css).toContain('hsl(var(--card))');
    expect(result.css).toContain('hsl(var(--muted-foreground))');
    expect(result.css).toContain('hsl(var(--background))');
    // borderRadius is wired to --radius, so the same switch drives shape.
    expect(result.css).toContain('calc(var(--radius) - 2px)');
  });
});
