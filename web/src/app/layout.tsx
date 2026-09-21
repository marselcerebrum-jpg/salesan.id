import type { Metadata, Viewport } from 'next';
import { Inter } from 'next/font/google';

import { ThemeProvider, themeBootstrapScript } from '@/lib/theme';

import './globals.css';

const inter = Inter({
  subsets: ['latin'],
  variable: '--font-inter',
  display: 'swap',
});

export const metadata: Metadata = {
  title: 'salesan.id · Omnichannel',
  description: 'Kelola akun WhatsApp dan balas percakapan pelanggan dari satu tempat.',
};

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  // The browser chrome around the app. The palette's slate rather than a
  // green: it is the colour the page's own text is set in, so the phone's
  // status bar reads as part of the page instead of a band stuck above it.
  themeColor: '#262d2e',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="id" className={inter.variable} suppressHydrationWarning>
      <head>
        {/* Runs before first paint so dark mode never flashes white. */}
        <script dangerouslySetInnerHTML={{ __html: themeBootstrapScript }} />
      </head>
      <body className="font-sans antialiased">
        <ThemeProvider>{children}</ThemeProvider>
      </body>
    </html>
  );
}
