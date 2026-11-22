"use client";

import { useState } from 'react';
import { AlertTriangle, CheckCircle, XCircle, RefreshCw } from 'lucide-react';

interface ReconciliationItem {
  id: string;
  vendorDescription: string;
  matchedBoq: string;
  confidence: number;
  status: 'PENDING_REVIEW' | 'HUMAN_APPROVED' | 'OVERRIDDEN';
}

export default function ReconcilePage({ params }: { params: { quotationId: string } }) {
  const [items, setItems] = useState<ReconciliationItem[]>([
    { id: 'REC-01', vendorDescription: 'C30 Pompalı Hazır Beton Dökümü', matchedBoq: 'CONC-01: C30 Structural Concrete', confidence: 0.7845, status: 'PENDING_REVIEW' },
    { id: 'REC-02', vendorDescription: 'Ø12 S420 Nervürlü Çelik', matchedBoq: 'STEEL-02: Ribbed Reinforcement Bar 12mm', confidence: 0.9210, status: 'HUMAN_APPROVED' }
  ]);

  const handleApprove = (id: string) => {
    setItems(items.map(item => item.id === id ? { ...item, status: 'HUMAN_APPROVED' } : item));
  };

  return (
    <div className="space-y-6">
      {/* Console Header */}
      <div className="bg-amber-50 border border-amber-200 p-4 rounded-lg flex items-center justify-between">
        <div className="flex items-center space-x-3">
          <AlertTriangle className="text-amber-600" size={24} />
          <div>
            <h4 className="text-sm font-semibold text-amber-900">Human-in-the-Loop Queue ({params.quotationId})</h4>
            <p className="text-xs text-amber-700">Items with confidence scores between 60% and 84% require manual verification.</p>
          </div>
        </div>
        <span className="text-xs bg-amber-200 text-amber-900 font-bold px-3 py-1 rounded-full">Action Required</span>
      </div>

      {/* Reconciliation Queue Table */}
      <div className="bg-white border border-gray-200 rounded-lg shadow-sm overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-200 bg-gray-50">
          <h3 className="font-semibold text-sm text-gray-800">Levenshtein & Hybrid Scoring Matches</h3>
        </div>
        <table className="w-full text-sm text-left">
          <thead className="bg-gray-100 text-gray-600 uppercase text-xs">
            <tr>
              <th className="px-6 py-3">Vendor Raw Description</th>
              <th className="px-6 py-3">Matched Master BOQ Item</th>
              <th className="px-6 py-3">Confidence Score</th>
              <th className="px-6 py-3">Status</th>
              <th className="px-6 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
            {items.map((item) => (
              <tr key={item.id} className="hover:bg-gray-50 transition">
                <td className="px-6 py-4 font-medium text-gray-900">{item.vendorDescription}</td>
                <td className="px-6 py-4 text-gray-700">{item.matchedBoq}</td>
                <td className="px-6 py-4 font-mono font-semibold">
                  <span className={item.confidence < 0.85 ? 'text-amber-600' : 'text-emerald-600'}>
                    {(item.confidence * 100).toFixed(2)}%
                  </span>
                </td>
                <td className="px-6 py-4">
                  {item.status === 'HUMAN_APPROVED' ? (
                    <span className="bg-green-100 text-green-800 text-xs px-2.5 py-1 rounded-full font-semibold">Approved</span>
                  ) : (
                    <span className="bg-amber-100 text-amber-800 text-xs px-2.5 py-1 rounded-full font-semibold">Pending Review</span>
                  )}
                </td>
                <td className="px-6 py-4 text-right space-x-2">
                  {item.status !== 'HUMAN_APPROVED' && (
                    <button onClick={() => handleApprove(item.id)} className="bg-emerald-600 text-white px-3 py-1.5 rounded text-xs hover:bg-emerald-700 transition">
                      Approve Match
                    </button>
                  )}
                  <button className="bg-gray-200 text-gray-700 px-3 py-1.5 rounded text-xs hover:bg-gray-300 transition">Override</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}