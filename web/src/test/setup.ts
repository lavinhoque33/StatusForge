import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

// Vitest runs with `globals: false`, so Testing Library cannot register its
// automatic cleanup hook; unmount rendered trees after every test instead.
afterEach(() => {
  cleanup();
});
