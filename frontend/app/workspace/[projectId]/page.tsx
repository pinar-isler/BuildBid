"use client";

import { useState } from 'react';
import { UploadCloud, FileText, CheckCircle2, Clock, AlertCircle } from 'lucide-react';

interface DocumentRow {
  id: string;
  name: string;
  type: 'BOQ' | 'QUOTATION' | 'TAPU';
  status: 'PARSED' | 'CACHED' | 'PENDING';
  hash: string;
  amount?: string;
}

export default function WorkspacePage({ params }: { params: { projectId: string } }) {
  const [documents, setDocuments] = useState<DocumentRow[]>([
    { id: 'DOC-01', name: 'Kadıköy_Master_BOQ_2026.xlsx', type: 'BOQ', status: 'PARSED', hash: 'e3b0c442...9fc1c' },
    { id: 'DOC-02', name: 'Kaya_Beton_Proposal.pdf', type: 'QUOTATION', status: 'CACHED', hash: '8f434346...2ab31', amount: '₺9,485,000.00' }
  ]);

  return (
    <div className="space-y-6">
      {/* KPI Header Grid */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Project Identifier</p>
          <p className="text-lg font-bold text-slate-800 mt-1">{params.projectId}</p>
        </div>
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Baseline Estimate</p>
          <p className="text-lg font-bold text-slate-800 mt-1">₺ 9,170,000.00</p>
        </div>
        <div className="bg-white p-4 border border-gray-200 rounded-lg shadow-sm">
          <p className="text-xs text-gray-500 uppercase font-semibold">Tender Status</p>
          <p className="text-sm font-semibold mt-1 flex items-center text-blue-600">
            <span className="w-2 h-2 rounded-full bg-blue-500 mr-2 animate-pulse"></span> Active Ingestion
          </p>
        </div>
        <div className="bg-white p-4 border border-green-200 bg-green-50 rounded-lg shadow-sm">
          <p className="text-xs text-green-700 uppercase font-semibold">SHA-256 Cache Engine</p>
          <p className="text-sm font-bold mt-1 text-green-800">Operational (&lt;500ms Hit)</p>
        </div>
      </div>

      {/* Upload Dropzones */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="border-2 border-dashed border-gray-300 rounded-lg p-8 flex flex-col items-center justify-center bg-white hover:border-blue-500 transition cursor-pointer group">
          <FileText className="text-gray-400 group-hover:text-blue-500 mb-3 transition" size={36} />
          <h3 className="font-semibold text-gray-700">Master BOQ or Title Deed Scan</h3>
          <p className="text-xs text-gray-500 mt-1">Upload project baseline Excel/CSV or Tapu PDF</p>
          <button className="mt-4 bg-slate-800 text-white px-4 py-2 rounded text-sm hover:bg-slate-900 transition">Browse Files</button>
        </div>

        <div className="border-2 border-dashed border-blue-300 rounded-lg p-8 flex flex-col items-center justify-center bg-blue-50/50 hover:border-blue-500 transition cursor-pointer group">
          <UploadCloud className="text-blue-500 mb-3 group-hover:scale-105 transition" size={36} />
          <h3 className="font-semibold text-blue-900">Subcontractor Proposal Packages</h3>
          <p className="text-xs text-blue-600 mt-1">Drop multi-vendor quote PDFs for AI extraction</p>
          <button className="mt-4 bg-blue-600 text-white px-4 py-2 rounded text-sm hover:bg-blue-700 transition">Upload Quotations</button>
        </div>
      </div>

      {/* Ingested Document Queue Table */}
      <div className="bg-white border border-gray-200 rounded-lg shadow-sm overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-200 bg-gray-50 flex justify-between items-center">
          <h3 className="font-semibold text-sm text-gray-800">Ingested Documents & Processing Queue</h3>
          <span className="text-xs bg-gray-200 px-2.5 py-1 rounded-full font-medium">{documents.length} Files Tracked</span>
        </div>
        <table className="w-full text-sm text-left">
          <thead className="bg-gray-100 text-gray-600 uppercase text-xs">
            <tr>
              <th className="px-6 py-3">Document Name</th>
              <th className="px-6 py-3">Category</th>
              <th className="px-6 py-3">SHA-256 Hash</th>
              <th className="px-6 py-3">Status</th>
              <th className="px-6 py-3 text-right">Extracted Total</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
            {documents.map((doc) => (
              <tr key={doc.id} className="hover:bg-gray-50 transition">
                <td className="px-6 py-4 font-medium text-gray-900 flex items-center">
                  <FileText size={16} className="text-blue-600 mr-2" />
                  {doc.name}
                </td>
                <td className="px-6 py-4"><span className="bg-gray-100 text-gray-700 px-2 py-1 rounded text-xs">{doc.type}</span></td>
                <td className="px-6 py-4 font-mono text-xs text-gray-500">{doc.hash}</td>
                <td className="px-6 py-4">
                  {doc.status === 'CACHED' ? (
                    <span className="flex items-center text-green-700 text-xs font-semibold"><CheckCircle2 size={14} className="mr-1" /> Cache Hit</span>
                  ) : (
                    <span className="flex items-center text-blue-700 text-xs font-semibold"><Clock size={14} className="mr-1" /> Parsed</span>
                  )}
                </td>
                <td className="px-6 py-4 text-right font-medium text-gray-900">{doc.amount || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}