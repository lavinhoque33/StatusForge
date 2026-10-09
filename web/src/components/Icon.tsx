import type { ReactNode } from 'react';

/**
 * Self-authored stroke icons on a 24 px grid (no icon dependency). Icons are
 * decorative: always `aria-hidden`, always next to visible text that carries
 * the meaning, never the only signal of a state.
 */
const paths = {
  overview: (
    <>
      <rect x="3.75" y="3.75" width="7" height="7" rx="1.75" />
      <rect x="13.25" y="3.75" width="7" height="7" rx="1.75" />
      <rect x="3.75" y="13.25" width="7" height="7" rx="1.75" />
      <rect x="13.25" y="13.25" width="7" height="7" rx="1.75" />
    </>
  ),
  monitors: <path d="M3 12.5h3.5l2.25-5.5 4.5 11 2.5-6.5 1.25 1H21" />,
  incidents: (
    <>
      <path d="M12 4.25 21 19.5H3L12 4.25Z" />
      <path d="M12 10v4" />
      <path d="M12 16.75v.25" />
    </>
  ),
  activity: (
    <>
      <path d="M9 6.5h11M9 12h11M9 17.5h7" />
      <circle cx="4.75" cy="6.5" r="1.25" />
      <circle cx="4.75" cy="12" r="1.25" />
      <circle cx="4.75" cy="17.5" r="1.25" />
    </>
  ),
  applications: (
    <>
      <path d="M12 3.75 3.75 8 12 12.25 20.25 8 12 3.75Z" />
      <path d="m3.75 12 8.25 4.25L20.25 12" />
      <path d="m3.75 16 8.25 4.25L20.25 16" />
    </>
  ),
  settings: (
    <>
      <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
      <circle cx="15" cy="7" r="2.25" />
      <circle cx="9" cy="17" r="2.25" />
    </>
  ),
  'check-circle': (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="m8.25 12.25 2.5 2.5 5-5.25" />
    </>
  ),
  'x-circle': (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="m9.25 9.25 5.5 5.5M14.75 9.25l-5.5 5.5" />
    </>
  ),
  'alert-circle': (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="M12 7.75v5" />
      <path d="M12 16.1v.15" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="M12 11v5.25" />
      <path d="M12 7.85v.15" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="M12 7.5V12l3 2" />
    </>
  ),
  bell: (
    <>
      <path d="M6.25 16.25v-5a5.75 5.75 0 0 1 11.5 0v5l1.5 1.75H4.75l1.5-1.75Z" />
      <path d="M10 20.25a2.1 2.1 0 0 0 4 0" />
    </>
  ),
  refresh: (
    <>
      <path d="M19.25 12a7.25 7.25 0 1 1-2.12-5.13" />
      <path d="M19.5 4.5v4.25h-4.25" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  table: (
    <>
      <rect x="3.75" y="4.75" width="16.5" height="14.5" rx="2" />
      <path d="M3.75 9.5h16.5M3.75 14.25h16.5M9.5 9.5v9.75" />
    </>
  ),
  chart: (
    <>
      <path d="M4 19.75h16" />
      <rect x="5.75" y="11" width="3" height="6" rx="1" />
      <rect x="10.5" y="5.5" width="3" height="11.5" rx="1" />
      <rect x="15.25" y="13" width="3" height="4" rx="1" />
    </>
  ),
  pause: (
    <>
      <rect x="6.75" y="5.25" width="3.5" height="13.5" rx="1" />
      <rect x="13.75" y="5.25" width="3.5" height="13.5" rx="1" />
    </>
  ),
  play: <path d="M8 5.5v13l10.5-6.5L8 5.5Z" />,
  heartbeat: (
    <>
      <circle cx="12" cy="12" r="2" />
      <path d="M7.75 7.75a6 6 0 0 0 0 8.5M16.25 7.75a6 6 0 0 1 0 8.5" />
      <path d="M5 5a9.9 9.9 0 0 0 0 14M19 5a9.9 9.9 0 0 1 0 14" />
    </>
  ),
  globe: (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="M3.25 12h17.5" />
      <path d="M12 3.25c2.3 2.4 3.5 5.3 3.5 8.75s-1.2 6.35-3.5 8.75c-2.3-2.4-3.5-5.3-3.5-8.75S9.7 5.65 12 3.25Z" />
    </>
  ),
  calendar: (
    <>
      <rect x="3.75" y="5.25" width="16.5" height="15" rx="2.25" />
      <path d="M3.75 10h16.5M8 3.25v4M16 3.25v4" />
    </>
  ),
  flag: (
    <>
      <path d="M5.5 20.5V4" />
      <path d="M5.5 4.5h11.75l-2.25 4 2.25 4H5.5" />
    </>
  ),
  trash: (
    <>
      <path d="M4.5 7h15" />
      <path d="M9.5 7V4.75h5V7" />
      <path d="M6.5 7l.9 12.25a1.5 1.5 0 0 0 1.5 1.25h6.2a1.5 1.5 0 0 0 1.5-1.25L17.5 7" />
    </>
  ),
  archive: (
    <>
      <rect x="3.5" y="4.25" width="17" height="4.75" rx="1.5" />
      <path d="M5.25 9v9.25a1.75 1.75 0 0 0 1.75 1.75h10a1.75 1.75 0 0 0 1.75-1.75V9" />
      <path d="M10 13h4" />
    </>
  ),
  edit: (
    <>
      <path d="M4.25 19.75h3.5L18.6 8.9a2.47 2.47 0 0 0-3.5-3.5L4.25 16.25v3.5Z" />
      <path d="m13.75 6.75 3.5 3.5" />
    </>
  ),
  external: (
    <>
      <path d="M14 4.25h5.75V10" />
      <path d="M19.75 4.25 11 13" />
      <path d="M18 14v4.25A1.75 1.75 0 0 1 16.25 20h-10.5A1.75 1.75 0 0 1 4 18.25V7.75A1.75 1.75 0 0 1 5.75 6H10" />
    </>
  ),
  database: (
    <>
      <ellipse cx="12" cy="6.25" rx="7.25" ry="2.75" />
      <path d="M4.75 6.25v5.75c0 1.5 3.25 2.75 7.25 2.75s7.25-1.25 7.25-2.75V6.25" />
      <path d="M4.75 12v5.75c0 1.5 3.25 2.75 7.25 2.75s7.25-1.25 7.25-2.75V12" />
    </>
  ),
  'chevron-right': <path d="m9.5 6 6 6-6 6" />,
  'chevron-down': <path d="m6 9.5 6 6 6-6" />,
  'arrow-left': <path d="M19 12H5M11 6l-6 6 6 6" />,
  key: (
    <>
      <circle cx="8.25" cy="15.75" r="4" />
      <path d="m11.1 12.9 8.4-8.4M16.5 7.5l2.25 2.25M14.25 9.75l1.75 1.75" />
    </>
  ),
  filter: <path d="M4 5.25h16l-6.25 7.5v5.75l-3.5 1.75v-7.5L4 5.25Z" />,
  copy: (
    <>
      <rect x="8.75" y="8.75" width="11.5" height="11.5" rx="2" />
      <path d="M15.25 8.75V5.5a1.75 1.75 0 0 0-1.75-1.75H5.5A1.75 1.75 0 0 0 3.75 5.5v8a1.75 1.75 0 0 0 1.75 1.75h3.25" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v11M7.5 10.75 12 15.25l4.5-4.5" />
      <path d="M4.5 19.75h15" />
    </>
  ),
  shield: <path d="M12 3.5 19.25 6v5.5c0 4.4-3 7.6-7.25 9-4.25-1.4-7.25-4.6-7.25-9V6L12 3.5Z" />,
  link: (
    <>
      <path d="M10 14a4.25 4.25 0 0 0 6 0l3-3a4.25 4.25 0 0 0-6-6l-1 1" />
      <path d="M14 10a4.25 4.25 0 0 0-6 0l-3 3a4.25 4.25 0 0 0 6 6l1-1" />
    </>
  ),
  pulse: <path d="M2.75 12h4l2-5.25 3.75 10.5 2.75-7.5 1.5 2.25h4.5" />,
  inbox: (
    <>
      <path d="M3.75 13.5 6.4 5.6a1.75 1.75 0 0 1 1.66-1.2h7.88a1.75 1.75 0 0 1 1.66 1.2l2.65 7.9" />
      <path d="M3.75 13.5v4.75A1.75 1.75 0 0 0 5.5 20h13a1.75 1.75 0 0 0 1.75-1.75V13.5h-4.75l-1.25 2.25h-4.5L8.5 13.5H3.75Z" />
    </>
  ),
  compass: (
    <>
      <circle cx="12" cy="12" r="8.75" />
      <path d="m15.25 8.75-1.9 4.6-4.6 1.9 1.9-4.6 4.6-1.9Z" />
    </>
  ),
  server: (
    <>
      <rect x="3.75" y="4.25" width="16.5" height="6.5" rx="1.75" />
      <rect x="3.75" y="13.25" width="16.5" height="6.5" rx="1.75" />
      <path d="M7.5 7.5h.01M7.5 16.5h.01" />
    </>
  ),
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof paths;

export function Icon({
  name,
  size = 18,
  className,
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      className={className === undefined ? 'icon' : `icon ${className}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {paths[name]}
    </svg>
  );
}
