package resources_test

//
//func TestEnvironment(t *testing.T) {
//	ctx := context.Background()
//	Dir := t.TempDir()
//
//	defer func() {
//		os.RemoveAll(Dir)
//	}()
//
//	var action actions.Action
//	var err error
//
//	action, err = action.NewActionNew(ctx, &actionsv0.New{
//		Name: "test-",
//		Path: Dir,
//	})
//	out, err := action.Run(ctx)
//require.NoError(t, err)
//	 := shared.Must(actions.As[resources.](out))
//
//	action, err = actionenviroment.NewActionAddEnvironment(ctx, &actionsv0.AddEnvironment{
//		Name:        "test-environment",
//		Path: .Dir(),
//	})
//require.NoError(t, err)
//	_, err = action.Run(ctx)
//require.NoError(t, err)
//
//	// Make sure the environment is created
//	content, err := os.ReadFile(path.Join(.Dir(), resources.ConfigurationName))
//require.NoError(t, err)
//	require.Contains(t, string(content), "name: test-environment")
//}
