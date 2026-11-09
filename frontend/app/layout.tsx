import type { Metadata } from 'next';
import './globals.css';
import TopNav from '@/components/TopNav';

export const metadata: Metadata = {
  title: 'BuildBid Dashboard',
  description: 'Automated decision-support and tender analysis platform',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>
        {/* Global Top Header */}
        <header className="bg-slate-900 text-white p-4 flex justify-between items-center shadow-md">
          <div className="flex items-center space-x-6">
            <h1 className="text-xl font-bold tracking-wide">BuildBid</h1>
            <span className="text-slate-500">|</span>
            <h2 className="text-sm font-medium text-slate-300">Active Tender Workspace</h2>
          </div>
          <div className="text-sm bg-slate-800 border border-slate-700 px-4 py-2 rounded-lg flex items-center space-x-2">
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
            <span className="text-slate-200">Procurement Engineer</span>
          </div>
        </header>

        {/* Persistent Client Navigation */}
        <TopNav projectId="PRJ-1704829104" />

        {/* Dynamic Page Content Area */}
        <main className="p-6 max-w-7xl mx-auto">
          {children}
        </main>
      </body>
    </html>
  );
}