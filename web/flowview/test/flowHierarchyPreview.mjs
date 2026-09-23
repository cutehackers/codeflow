import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { flowHierarchyFixture } from './flowHierarchyFixture.mjs';
const variants = ['hierarchy','baseline','missing-source','cycle','many','invalid'];
const port = Number(process.env.FLOWVIEW_PREVIEW_PORT || 4782);
createServer(async(req,res)=>{
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  res.setHeader('Cache-Control','no-store');
  if (url.pathname.startsWith('/api/')) {
    res.setHeader('Content-Type','application/json');
    let result;
    if(url.pathname==='/api/views') result={views:variants.map(viewId=>({viewId,title:`화면 검증 · ${viewId}`}))};
    else if(url.pathname==='/api/view/compare') result={changes:[{kind:'changed_rule',targetStepId:'validate',summary:'검증 조건 변경'}]};
    else if(url.pathname==='/api/semantic/labels') result={labels:[]};
    else if(url.pathname==='/api/view' || url.pathname==='/api/task/view') result=flowHierarchyFixture(url.searchParams.get('viewId') || 'hierarchy');
    else {res.statusCode=404;result={message:'검증 서버에서 제공하지 않는 API'};}
    res.end(JSON.stringify(result));
  } else {
    try {res.setHeader('Content-Type','text/html; charset=utf-8');res.end(await readFile(new URL('../dist/index.html',import.meta.url)));}
    catch {res.statusCode=500;res.end('먼저 npm run build를 실행하세요.');}
  }
}).listen(port,'127.0.0.1',()=>process.stdout.write(`FlowView UI contract fixture: http://127.0.0.1:${port}/?viewId=hierarchy\n`));
