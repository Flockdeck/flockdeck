const {Terminal}=require('@xterm/headless');
const {Unicode11Addon}=require('@xterm/addon-unicode11');
const fs=require('fs');
function render(buf,cols,rows,u11){
  const t=new Terminal({cols,rows,allowProposedApi:true,scrollback:0});
  if(u11){t.loadAddon(new Unicode11Addon());t.unicode.activeVersion='11';}
  return new Promise(r=>t.write(buf,()=>{
    const lines=[];for(let i=0;i<rows;i++){const l=t.buffer.active.getLine(i);lines.push(l?l.translateToString(true):'')}
    r(lines.join('\n').replace(/\n+$/,''))}));
}
module.exports={render};
if(require.main===module){(async()=>{
 const [,,file,cols,rows,u11,direct]=process.argv;
 const buf=fs.readFileSync(file);
 console.log(await render(buf,+cols,+rows,u11==='1'));
})()}
