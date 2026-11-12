import re,difflib,json
L=["Your business already runs on chat. Orders, voice notes, prices, all in one pile.",
"What if the chat could run it with you?",
"Every morning at eight, Taskiem sends one card. Yesterday's sales, who owes you, what's running low, and what goes out today.",
"What needs you comes with the next step on it. Reorder the rice. Approve a bill.",
"Writing to a supplier? Tag a product, a service, or a business, right in the message.",
"Open any tool without leaving the chat. Change a price, tap Save.",
"Every change is written down, wherever you work.",
"Orders and invoices between businesses arrive as cards. Accept, and follow it from placed to delivered.",
"Both sides see the same order. Nothing to retype.",
"Your staff see what their role needs. Tunde sells, counts stock, and hands over the shift, within the limits you set.",
"Light by day, dark by night, and always, how did we do.",
"Find businesses near you, connect, and trade with them.",
"Rivers Foods wants twenty party packs. From ten packs it is forty-three thousand each. They offer eight hundred thousand.",
"Tunde counters at eight hundred and forty. They answer on WhatsApp: eight twenty. Offers and counters, from the app or WhatsApp.",
"Eight twenty is under Ada's lowest price, so it waits for her OK. She agrees.",
"Run your business from the chat. Turn on Chat tools."]
norm=lambda w:re.sub(r"[^a-z0-9]","",w.lower())
sw=[];li=[]
for i,l in enumerate(L):
    for w in l.split():
        n=norm(w)
        if n: sw.append(n);li.append(i)
rows=[l.split("\t") for l in open("words.tsv").read().strip().split("\n")]
tw=[norm(r[2]) for r in rows]
sm=difflib.SequenceMatcher(None,sw,tw,autojunk=False)
first={};last={}
for a,b,n in sm.get_matching_blocks():
    for k in range(n):
        i=li[a+k]; t0=float(rows[b+k][0]); t1=float(rows[b+k][1])
        first.setdefault(i,t0); last[i]=t1
print("match ratio",round(sm.ratio(),3))
sc=[7,5,12,8,10,12,6,12,8,12,6,10,10,10,7,7]
for i in range(16):
    print(i+1, first.get(i), last.get(i), None if i not in first else round(last[i]-first[i],1), "scene",sc[i])
json.dump({"first":first,"last":last},open("lines.json","w"))
# unmatched script words per line
print(len(sw),len(tw))
