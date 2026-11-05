import json, sys
from palettes import P
def hx(h): h=h.lstrip('#'); return tuple(int(h[i:i+2],16) for i in (0,2,4))
def lin(c):
    c/=255; return c/12.92 if c<=0.03928 else ((c+0.055)/1.055)**2.4
def L(rgb): r,g,b=[lin(x) for x in rgb]; return 0.2126*r+0.7152*g+0.0722*b
def ratio(a,b):
    la,lb=L(a),L(b); hi,lo=max(la,lb),min(la,lb); return (hi+0.05)/(lo+0.05)
def comp(fg,a,bg): return tuple(round(f*a+b*(1-a)) for f,b in zip(fg,bg))
def tohex(t): return '#%02X%02X%02X'%t
def measure(theme):
    base=hx(theme['bg-base']); raised=hx(theme['bg-raised'])
    g,ga=theme['surface-glass']; glass=comp(hx(g),ga,base)
    bgs={'bg-base':base,'bg-raised':raised,'surface-glass':glass}
    out={'_glass_composited_on_base':tohex(glass)}
    for k in ['text-primary','text-secondary','text-muted','accent','status-downloading','status-missing','status-upgraded','status-failed','status-available']:
        out[k]={n:round(ratio(hx(theme[k]),v),2) for n,v in bgs.items()}
    out['accent-contrast-on-accent']=round(ratio(hx(theme['accent-contrast']),hx(theme['accent'])),2)
    bc,ba=theme['border']; out['border-on-bg-raised (decorative)']=round(ratio(comp(hx(bc),ba,raised),raised),2)
    return out
if __name__=='__main__':
    res={}
    for d in P:
        for t in ('dark','light'):
            m=measure(P[d][t]); res[f'{d}-{t}']=m
            fails=[]
            for k,v in m.items():
                if isinstance(v,dict):
                    thr=4.5
                    mn=min(v.values())
                    if mn<thr: fails.append(f'{k}={mn}')
            print(f'{d}-{t}', 'glass', m['_glass_composited_on_base'], 'acc-on-acc', m['accent-contrast-on-accent'], 'FAIL<4.5:' , fails)
            for k,v in m.items():
                if isinstance(v,dict): print('   %-20s %s'%(k, '  '.join(f'{n}:{x}' for n,x in v.items())))
    json.dump(res,open('contrast.json','w'),indent=1)
