"use client";

import { useState } from 'react';
import { BarChart2, Download, Star, AlertCircle } from 'lucide-react';

export default function ComparisonMatrixPage({ params }: { params: { projectId: string } }) {
  const [selectedDivision, setSelectedDivision] = useState('ALL');

  return (
    <div className="space-y-6">
      {/* Matrix Toolbar */}
      <div className="flex flex-col md:flex-row justify-between items-center gap-4">
        <div className="flex space-x-2">
          <button onClick={() => setSelectedDivision('ALL')} className={`px-4 py-2 rounded text-sm font-medium ${selectedDivision === 'ALL' ? 'bg-slate-900 text-white' : 'bg-white border text-gray-600'}`}>All Divisions</button>
          <button onClick={() => setSelectedDivision('CONCRETE')} className={`px-4 py-2 rounded text-sm font-medium ${selectedDivision === 'CONCRETE' ? 'bg-slate-900 text-white' : 'bg-white border text-gray-600'}`}>Concrete Works</button>
          <button onClick={() => setSelectedDivision('STEEL')} className={`px-4 py-2 rounded text-sm font-medium ${selectedDivision === 'STEEL' ? 'bg-slate-900 text-white' : 'bg-white border text-gray-600'}`}>Steel Reinforcement</button>
        </div>
        <div className="flex space-x-2">
          <button className="bg-emerald-600 text-white px-4 py-2 rounded text-sm flex items-center hover:bg-emerald-700 transition">
            <Download size={14} className="mr-2" /> Export Excel
          </button>
          <button className="bg-red-600 text-white px-4 py-2 rounded text-sm flex items-center hover:bg-red-700 transition">
            <Download size={14} className="mr-2" /> Export PDF
          </button>
        </div>
      </div>

      {/* Comparison Matrix Table */}
      <div className="bg-white border border-gray-200 rounded-lg shadow-sm overflow-x-auto">
        <div className="px-6 py-4 border-b border-gray-200 bg-gray-50 flex justify-between items-center">
          <h3 className="font-semibold text-sm text-gray-800">Deterministic Bid Comparison Matrix ({params.projectId})</h3>
          <span className="text-xs text-gray-500">Calculated via Go float64 Deterministic Engine</span>
        </div>
        <table className="w-full text-sm text-left">
          <thead className="bg-gray-100 text-gray-600 uppercase text-xs">
            <tr>
              <th className="px-6 py-3">BOQ Item Code & Description</th>
              <th className="px-6 py-3">Baseline Est. (₺)</th>
              <th className="px-6 py-3 bg-blue-50 text-blue-900">Kaya Beton A.Ş.</th>
              <th className="px-6 py-3">Marmara Çimento</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
            <tr className="hover:bg-gray-50 transition">
              <td className="px-6 py-4 font-medium text-gray-900">CONC-01: C30 Structural Concrete (2000 m³)</td>
              <td className="px-6 py-4 font-mono">₺2,500.00</td>
              <td className="px-6 py-4 font-mono bg-blue-50/50 font-semibold text-blue-900">
                ₺2,700.00 <span className="text-xs text-amber-600 ml-1">(+8.0%)</span>
              </td>
              <td className="px-6 py-4 font-mono flex items-center">
                ₺2,450.00 <span className="text-xs text-emerald-600 ml-2 font-bold flex items-center"><Star size={12} className="fill-emerald-600 mr-0.5" /> Lowest</span>
              </td>
            </tr>
            <tr className="hover:bg-gray-50 transition">
              <td className="px-6 py-4 font-medium text-gray-900">STEEL-01: Rebar S420 (150 tons)</td>
              <td className="px-6 py-4 font-mono">₺22,000.00</td>
              <td className="px-6 py-4 font-mono bg-blue-50/50 font-semibold text-red-600">
                OMITTED <span className="text-[10px] bg-red-100 text-red-800 px-1.5 py-0.5 rounded ml-1">Hidden Risk</span>
              </td>
              <td className="px-6 py-4 font-mono text-emerald-700">₺21,500.00</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}