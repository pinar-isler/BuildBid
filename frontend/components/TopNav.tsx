"use client";

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { FileText, Map, List, BarChart2 } from 'lucide-react';

export default function TopNav({ projectId = "PRJ-1704829104" }: { projectId?: string }) {
  const pathname = usePathname();

  const tabs = [
    { href: `/workspace/${projectId}`, label: 'Ingestion Workspace', icon: FileText, matchKey: 'workspace' },
    { href: `/title-deed-analysis/${projectId}`, label: 'Title Deed (Tapu)', icon: Map, matchKey: 'title-deed-analysis' },
    { href: `/reconcile/QT-PENDING`, label: 'Item Reconciliation', icon: List, matchKey: 'reconcile' },
    { href: `/comparison-matrix/${projectId}`, label: 'Comparison Matrix', icon: BarChart2, matchKey: 'comparison-matrix' },
  ];

  return (
    <nav className="bg-white border-b border-gray-200 px-6 flex space-x-8 shadow-sm">
      {tabs.map((tab) => {
        const isActive = pathname.includes(tab.matchKey);
        return (
          <Link
            key={tab.href}
            href={tab.href}
            className={`flex items-center space-x-2 py-4 border-b-2 font-medium text-sm transition-colors ${
              isActive
                ? 'border-blue-600 text-blue-600'
                : 'border-transparent text-gray-500 hover:text-gray-800 hover:border-gray-300'
            }`}
          >
            <tab.icon size={16} />
            <span>{tab.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}