'use strict';

const assert = require('assert');
const { scanSource, scanFunctionBody } = require('../lib/scanner');

function run() {
  console.log('--- Test Suite: React Functional Components & Nested Handlers ---');

  // TS-SCAN-FE-01: React arrow component with nested async handlers and callbacks
  const code1 = `
import React, { useState } from 'react';

export const LoginPage = (props: LoginPageProps) => {
  const [loading, setLoading] = useState(false);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    await authService.login(e.target.email.value);
  };

  const onGoogleLogin = async () => {
    await authService.socialLogin('google');
  };

  function onReset() {
    setLoading(false);
  }

  return (
    <form onSubmit={onSubmit}>
      <button onClick={onGoogleLogin}>Google</button>
      <button type="reset" onClick={onReset}>Reset</button>
    </form>
  );
};
`;
  const scan1 = scanSource(code1);
  assert.strictEqual(scan1.topLevelFunctions.length, 4, 'Should extract LoginPage and 3 nested handlers');

  const loginPage = scan1.topLevelFunctions.find(f => f.name === 'LoginPage');
  assert(loginPage, 'LoginPage must be registered');
  assert.strictEqual(loginPage.localName, 'LoginPage');
  assert.strictEqual(loginPage.parentScope, '');
  assert.strictEqual(code1[loginPage.bodyEnd], '}');

  const onSubmit = scan1.topLevelFunctions.find(f => f.name === 'LoginPage.onSubmit');
  assert(onSubmit, 'LoginPage.onSubmit must be registered');
  assert.strictEqual(onSubmit.localName, 'onSubmit');
  assert.strictEqual(onSubmit.parentScope, 'LoginPage');
  assert.strictEqual(onSubmit.isAsync, true);
  assert.strictEqual(code1[onSubmit.bodyEnd], '}');
  assert(code1.substring(onSubmit.bodyStart, onSubmit.bodyEnd).includes('authService.login'));

  const onGoogle = scan1.topLevelFunctions.find(f => f.name === 'LoginPage.onGoogleLogin');
  assert(onGoogle, 'LoginPage.onGoogleLogin must be registered');
  assert.strictEqual(onGoogle.localName, 'onGoogleLogin');
  assert.strictEqual(onGoogle.parentScope, 'LoginPage');
  assert.strictEqual(onGoogle.isAsync, true);

  const onReset = scan1.topLevelFunctions.find(f => f.name === 'LoginPage.onReset');
  assert(onReset, 'LoginPage.onReset must be registered');
  assert.strictEqual(onReset.localName, 'onReset');
  assert.strictEqual(onReset.parentScope, 'LoginPage');
  assert.strictEqual(onReset.isAsync, false);

  // TS-SCAN-FE-02: Standard function component with useCallback and memoized callbacks
  const code2 = `
export default function RegisterForm() {
  const onFormSubmit = useCallback(async (data: FormData) => {
    await submitForm(data);
  }, []);

  const onValidate = (field: string) => {
    return field.length > 0;
  };

  return <div />;
}
`;
  const scan2 = scanSource(code2);
  assert.strictEqual(scan2.topLevelFunctions.length, 3, 'Should extract RegisterForm and 2 handlers');
  const regForm = scan2.topLevelFunctions.find(f => f.name === 'RegisterForm');
  assert(regForm, 'RegisterForm must be registered');
  const onFormSubmit = scan2.topLevelFunctions.find(f => f.name === 'RegisterForm.onFormSubmit');
  assert(onFormSubmit, 'RegisterForm.onFormSubmit must be registered');
  assert.strictEqual(onFormSubmit.isAsync, true);
  assert.strictEqual(code2[onFormSubmit.bodyEnd], '}');

  // TS-SCAN-FE-03: React.memo and forwardRef wrapped functional components
  const code3 = `
export const MemoizedCard = React.memo((props: CardProps) => {
  const onCardClick = () => {
    props.onClick();
  };
  return <div onClick={onCardClick} />;
});

export const CustomInput = forwardRef<HTMLInputElement, InputProps>((props, ref) => {
  const onInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    props.onChange(e.target.value);
  };
  return <input ref={ref} onChange={onInputChange} />;
});
`;
  const scan3 = scanSource(code3);
  assert.strictEqual(scan3.topLevelFunctions.length, 4, 'Should extract 2 wrapped components and their handlers');
  assert(scan3.topLevelFunctions.some(f => f.name === 'MemoizedCard'));
  assert(scan3.topLevelFunctions.some(f => f.name === 'MemoizedCard.onCardClick'));
  assert(scan3.topLevelFunctions.some(f => f.name === 'CustomInput'));
  assert(scan3.topLevelFunctions.some(f => f.name === 'CustomInput.onInputChange'));

  // TS-SCAN-FE-04: Deep 3-level nesting (Component -> Handler -> Inner Callback -> Helper)
  const code4 = `
export const CheckoutPage = () => {
  const onCheckout = async () => {
    const validateCart = () => {
      const checkStock = () => {
        return true;
      };
      return checkStock();
    };
    if (validateCart()) {
      await processPayment();
    }
  };
};
`;
  const scan4 = scanSource(code4);
  assert.strictEqual(scan4.topLevelFunctions.length, 4, 'Should extract 4 functions across 3 nesting levels');
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage'));
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage.onCheckout'));
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage.onCheckout.validateCart'));
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage.onCheckout.validateCart.checkStock'));

  // TS-SCAN-FE-05: Custom hooks with inner functions
  const code5 = `
export function useAuth() {
  const [user, setUser] = useState(null);

  const login = useCallback(async (credentials: Credentials) => {
    const res = await api.post('/login', credentials);
    setUser(res.data);
  }, []);

  const logout = async () => {
    await api.post('/logout');
    setUser(null);
  };

  return { user, login, logout };
}
`;
  const scan5 = scanSource(code5);
  assert.strictEqual(scan5.topLevelFunctions.length, 3, 'Should extract useAuth and 2 methods');
  assert(scan5.topLevelFunctions.some(f => f.name === 'useAuth'));
  assert(scan5.topLevelFunctions.some(f => f.name === 'useAuth.login'));
  assert(scan5.topLevelFunctions.some(f => f.name === 'useAuth.logout'));

  // TS-SCAN-FE-06: Direct unit test for scanFunctionBody helper
  const rawBody = `
    const onClick = () => {
      console.log('clicked');
    };
    function onDismiss() {
      close();
    }
  `;
  const nested = scanFunctionBody(rawBody, 0, rawBody.length, 'ModalView');
  assert.strictEqual(nested.length, 2);
  assert.strictEqual(nested[0].name, 'ModalView.onClick');
  assert.strictEqual(nested[1].name, 'ModalView.onDismiss');

  console.log('✓ All React Functional Components & Nested Handlers tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
