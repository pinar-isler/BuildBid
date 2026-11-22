import Link from 'next/link';
import { ArrowRight, FileText, FolderPlus } from 'lucide-react';

export default function HomePage() {
  // Default project ID matching your workspace route
  const defaultProjectId = "PRJ-1704829104";

  return (
    <div className="py-12 px-4 max-w-4xl mx-auto text-center space-y-8">
      <h1 className="text-4xl font-extrabold text-slate-900 tracking-tight">
        Welcome to BuildBid
      </h1>
      <p className="text-lg text-gray-600 max-w-2xl mx-auto">
        Automated construction procurement decision-support, vendor quotation extraction, and deterministic comparison engine.
      </p>
    </div>
  );
}