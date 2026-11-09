package chatpage_test

import (
	"net/url"
	"strings"
	"testing"
)

func TestReplyKeyboardAndForceReplyInBrowser(t *testing.T) {
	chrome := requireChrome(t)
	st := newStand(t)
	if err := st.send(url.Values{"text": {"Choose"}, "reply_markup": {`{"keyboard":[[{"text":"Photo"},{"text":"Video"},{"text":"Wallet"}]],"one_time_keyboard":true,"resize_keyboard":true}`}}); err != nil {
		t.Fatal(err)
	}
	if err := st.send(url.Values{"text": {"Model"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Model","callback_data":"model"}]]}`}}); err != nil {
		t.Fatal(err)
	}
	got := st.tap(t, chrome, `
 doc.getElementById('user').value='77'; doc.getElementById('username').value='bob';
 (await until(()=>[...doc.querySelectorAll('#replyKeyboard button')].find(b=>b.textContent==='Video'))).click();
 await until(()=>doc.getElementById('replyKeyboard').hidden);
 doc.getElementById('keyboardToggle').click();
 if(doc.getElementById('replyKeyboard').hidden) throw new Error('cannot reopen one-time menu');
 const res=await (await win.fetch('/bot1/getUpdates')).json();
 out.value=res.result;
 `)
	if !strings.Contains(string(got.Value), `"text":"Video"`) || !strings.Contains(string(got.Value), `"id":77`) || strings.Contains(string(got.Value), "callback_query") {
		t.Fatal(string(got.Value))
	}
	if err := st.send(url.Values{"text": {"What should change?"}, "reply_markup": {`{"force_reply":true,"input_field_placeholder":"Describe changes"}`}}); err != nil {
		t.Fatal(err)
	}
	got = st.tap(t, chrome, `
 await until(()=>doc.getElementById('text').placeholder==='Describe changes');
 doc.getElementById('text').value='Brighter';
 doc.getElementById('sendForm').requestSubmit();
 await until(()=>[...doc.querySelectorAll('#log .msg')].find(b=>b.textContent.includes('Brighter')));
 out.value=await (await win.fetch('/bot1/getUpdates')).json();
 `)
	if !strings.Contains(string(got.Value), `"reply_to_message"`) {
		t.Fatal(string(got.Value))
	}
	if err := st.send(url.Values{"text": {"Removed"}, "reply_markup": {`{"remove_keyboard":true}`}}); err != nil {
		t.Fatal(err)
	}
	got = st.tap(t, chrome, `await until(()=>doc.getElementById('keyboardToggle').hidden);out.value={removed:doc.getElementById('replyKeyboard').hidden};`)
	if !strings.Contains(string(got.Value), `"removed":true`) {
		t.Fatal(string(got.Value))
	}
}
