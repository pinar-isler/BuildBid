"use client";

import { useState } from 'react';
import { Map, ShieldCheck, UserCheck } from 'lucide-react';

interface Shareholder {
  id: string;
  maskedTc: string;
  name: string;
  fraction: string;
  areaM2: number;
}

export default function TitleDeedAnalysisPage({ params }: { params: { projectId: string } }) {
  const [deedInfo] = useState({
    city: 'İstanbul',
    district: 'Kadıköy',
    blockAda: '1452',
    parcelParsel: '12',
    totalAreaM2: 3450.00,
  });

  const [shareholders] = useState<Shareholder[]>([
    { id: 'SH-1', maskedTc: 'TR104*****92', name: 'Ahmet Yılmaz', fraction: '1/2', areaM2: 1725.00 },
    { id: 'SH-2', maskedTc: 'TR992*****14', name: 'Mehmet Demir', fraction: '1/2', areaM2: 1725.00 }
  ]);

  return (
    <div className="space-y-6">
      {/* KVKK Compliance Banner */}
      <div className="bg-emerald-50 border border-emerald-200 p-4 rounded-lg flex items-center justify-between">
        <div className="flex items-center space-x-3">
          <ShieldCheck className="text-emerald-600" size={24} />
          <div>
            <h4 className="text-sm font-semibold text-emerald-900">KVKK Law No. 6698 Compliance Active</h4>
            <p className="text-xs text-emerald-700">Turkish National ID numbers are securely masked in memory and storage.</p>
          </div>
        </div>
        <span className="text-xs bg-emerald-200 text-emerald-900 font-bold px-3 py-1 rounded-full">Scrubbed</span>
      </div>

      {/* Cadastral Metadata Grid */}
      <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">City / District</p>
          <p className="text-base font-medium text-gray-800 mt-1">{deedInfo.city} / {deedInfo.district}</p>
        </div>
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Block (Ada)</p>
          <p className="text-base font-medium text-gray-800 mt-1">{deedInfo.blockAda}</p>
        </div>
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Parcel (Parsel)</p>
          <p className="text-base font-medium text-gray-800 mt-1">{deedInfo.parcelParsel}</p>
        </div>
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Total Surface Area</p>
          <p className="text-base font-medium text-gray-800 mt-1">{deedInfo.totalAreaM2.toLocaleString()} m²</p>
        </div>
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Project Reference</p>
          <p className="text-base font-medium text-blue-600 mt-1">{params.projectId}</p>
        </div>
      </div>

      {/* Shareholder Equity Breakdown Table */}
      <div className="bg-white border border-gray-200 rounded-lg shadow-sm overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-200 bg-gray-50">
          <h3 className="font-semibold text-sm text-gray-800">Land Registry Shareholder Equity Distribution</h3>
        </div>
        <table className="w-full text-sm text-left">
          <thead className="bg-gray-100 text-gray-600 uppercase text-xs">
            <tr>
              <th className="px-6 py-3">Shareholder Name</th>
              <th className="px-6 py-3">Masked National ID (KVKK)</th>
              <th className="px-6 py-3">Ownership Fraction</th>
              <th className="px-6 py-3 text-right">Computed Net Area</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
            {shareholders.map((sh) => (
              <tr key={sh.id} className="hover:bg-gray-50 transition">
                <td className="px-6 py-4 font-medium text-gray-900 flex items-center">
                  <UserCheck size={16} className="text-emerald-600 mr-2" />
                  {sh.name}
                </td>
                <td className="px-6 py-4 font-mono text-xs text-gray-600">{sh.maskedTc}</td>
                <td className="px-6 py-4 font-semibold text-gray-800">{sh.fraction}</td>
                <td className="px-6 py-4 text-right font-medium text-gray-900">{sh.areaM2.toLocaleString()} m²</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}